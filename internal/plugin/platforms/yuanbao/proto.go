// proto.go — 元宝 WebSocket 协议编解码。
//
// 协议对齐 hermes-agent gateway/platforms/yuanbao_proto.py（手写 protobuf
// wire-format，encoding/binary 级别实现，不引入第三方 protobuf 库）。
//
// 每个 WebSocket 帧 = 一条 ConnMsg protobuf（标准 protobuf；magic+len 二进制
// 格式只用于 quic/tcp）：
//
//	ConnMsg { Head head=1; bytes data=2 }
//	Head    { uint32 cmd_type=1; string cmd=2; uint32 seq_no=3; string msg_id=4;
//	          string module=5; bool need_ack=6; int32 status=10 }
//
// data 为业务 payload（InboundMessagePush / SendC2CMessageReq /
// SendGroupMessageReq / ...，服务名前缀 yuanbao_openclaw_proxy）。
package yuanbao

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// protobuf wire types
const (
	wtVarint = 0
	wt64bit  = 1
	wtLen    = 2
	wt32bit  = 5
)

// conn 层常量（对齐 yuanbao_proto.py CMD_TYPE / CMD / MODULE）。
const (
	cmdTypeRequest  = 0 // 上行请求
	cmdTypeResponse = 1 // 请求回包
	cmdTypePush     = 2 // 下行推送
	cmdTypePushAck  = 3 // 推送 ACK

	cmdAuthBind = "auth-bind"
	cmdPing     = "ping"

	moduleConnAccess = "conn_access"
	bizPkg           = "yuanbao_openclaw_proxy" // TS client 使用短名（非完整包路径）

	instanceID = 17 // openclaw instance_id（固定值，对齐 HERMES_INSTANCE_ID）
)

var seqCounter atomic.Uint32

// nextSeqNo 生成递增序列号（uint32，溢出自然回绕，对齐 python next_seq_no）。
func nextSeqNo() uint32 { return seqCounter.Add(1) }

// ---- 编码工具 ----

func appendVarint(b []byte, v uint64) []byte {
	for {
		bits := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			return append(b, bits)
		}
		b = append(b, bits|0x80)
	}
}

func decodeVarint(data []byte) (uint64, int, error) {
	var result uint64
	shift := uint(0)
	for pos := 0; pos < len(data); pos++ {
		b := data[pos]
		result |= uint64(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			return result, pos + 1, nil
		}
		if shift >= 64 {
			return 0, 0, fmt.Errorf("varint too long")
		}
	}
	return 0, 0, fmt.Errorf("varint truncated")
}

// pbBuf 是一个 protobuf message 编码缓冲。字段编码语义对齐 python 端
// _encode_parts 的 kind：strField 总是编码（"S"）；strIf 非空才编码（"s"）；
// varintIf 非零才编码（"v"）；nested/bytesField 总是编码（"m"）。
type pbBuf struct{ b []byte }

func (p *pbBuf) varint(fn int, v uint64) {
	p.b = appendVarint(p.b, uint64(fn)<<3|wtVarint)
	p.b = appendVarint(p.b, v)
}

func (p *pbBuf) varintIf(fn int, v uint64) {
	if v != 0 {
		p.varint(fn, v)
	}
}

func (p *pbBuf) bytes(fn int, val []byte) {
	p.b = appendVarint(p.b, uint64(fn)<<3|wtLen)
	p.b = appendVarint(p.b, uint64(len(val)))
	p.b = append(p.b, val...)
}

func (p *pbBuf) str(fn int, s string) { p.bytes(fn, []byte(s)) }

func (p *pbBuf) strIf(fn int, s string) {
	if s != "" {
		p.str(fn, s)
	}
}

func (p *pbBuf) nested(fn int, inner []byte) {
	if len(inner) > 0 {
		p.bytes(fn, inner)
	}
}

// ---- 解码工具 ----

type pbField struct {
	num int
	wt  int
	val []byte // LEN / 64BIT / 32BIT 原始字节
	iv  uint64 // VARINT 值
}

type pbDict map[int][]pbField

func parseFields(data []byte) ([]pbField, error) {
	var fields []pbField
	pos := 0
	for pos < len(data) {
		tag, n, err := decodeVarint(data[pos:])
		if err != nil {
			return nil, err
		}
		pos += n
		wt := int(tag & 0x07)
		f := pbField{num: int(tag >> 3), wt: wt}
		switch wt {
		case wtVarint:
			v, n, err := decodeVarint(data[pos:])
			if err != nil {
				return nil, err
			}
			f.iv = v
			pos += n
		case wtLen:
			l, n, err := decodeVarint(data[pos:])
			if err != nil {
				return nil, err
			}
			pos += n
			if uint64(len(data)-pos) < l {
				return nil, fmt.Errorf("length-delimited field truncated")
			}
			f.val = data[pos : pos+int(l)]
			pos += int(l)
		case wt64bit:
			if len(data)-pos < 8 {
				return nil, fmt.Errorf("fixed64 truncated")
			}
			f.val = data[pos : pos+8]
			pos += 8
		case wt32bit:
			if len(data)-pos < 4 {
				return nil, fmt.Errorf("fixed32 truncated")
			}
			f.val = data[pos : pos+4]
			pos += 4
		default:
			return nil, fmt.Errorf("unknown wire type %d", wt)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func parseDict(data []byte) (pbDict, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, err
	}
	d := make(pbDict, len(fields))
	for _, f := range fields {
		d[f.num] = append(d[f.num], f)
	}
	return d, nil
}

// first 返回第一个 wire type 匹配的字段，无则 nil。
func (d pbDict) first(fn, wt int) *pbField {
	for i := range d[fn] {
		if d[fn][i].wt == wt {
			return &d[fn][i]
		}
	}
	return nil
}

func (d pbDict) getString(fn int) string {
	if f := d.first(fn, wtLen); f != nil {
		return string(f.val)
	}
	return ""
}

func (d pbDict) getVarint(fn int) uint64 {
	if f := d.first(fn, wtVarint); f != nil {
		return f.iv
	}
	return 0
}

func (d pbDict) getBytes(fn int) []byte {
	if f := d.first(fn, wtLen); f != nil {
		return f.val
	}
	return nil
}

func (d pbDict) repeatedBytes(fn int) [][]byte {
	var out [][]byte
	for _, f := range d[fn] {
		if f.wt == wtLen {
			out = append(out, f.val)
		}
	}
	return out
}

// ---- ConnMsg 层 ----

func encodeHead(cmdType int, cmd string, seqNo uint32, msgID, module string, needAck bool, status int) []byte {
	var p pbBuf
	p.varintIf(1, uint64(uint32(cmdType)))
	p.strIf(2, cmd)
	p.varintIf(3, uint64(seqNo))
	p.strIf(4, msgID)
	p.strIf(5, module)
	if needAck {
		p.varint(6, 1)
	}
	p.varintIf(10, uint64(uint32(status)))
	return p.b
}

// encodeConnMsgFull 编码完整 ConnMsg（含 cmd/msg_id/module 等 head 字段）。
func encodeConnMsgFull(cmdType int, cmd string, seqNo uint32, msgID, module string, data []byte, needAck bool) []byte {
	var p pbBuf
	p.bytes(1, encodeHead(cmdType, cmd, seqNo, msgID, module, needAck, 0))
	if len(data) > 0 {
		p.bytes(2, data)
	}
	return p.b
}

func encodeConnMsg(cmdType int, cmd string, seqNo uint32, msgID, module string, data []byte) []byte {
	return encodeConnMsgFull(cmdType, cmd, seqNo, msgID, module, data, false)
}

// connMsg 是解码后的 ConnMsg。
type connMsg struct {
	cmdType int
	cmd     string
	seqNo   uint32
	msgID   string
	module  string
	needAck bool
	status  uint64
	data    []byte
}

func decodeConnMsg(data []byte) (*connMsg, error) {
	d, err := parseDict(data)
	if err != nil {
		return nil, err
	}
	hb := d.getBytes(1)
	if hb == nil {
		return nil, fmt.Errorf("conn msg missing head")
	}
	h, err := parseDict(hb)
	if err != nil {
		return nil, err
	}
	m := &connMsg{
		cmdType: int(h.getVarint(1)),
		cmd:     h.getString(2),
		seqNo:   uint32(h.getVarint(3)),
		msgID:   h.getString(4),
		module:  h.getString(5),
		needAck: h.getVarint(6) != 0,
		status:  h.getVarint(10),
		data:    d.getBytes(2),
	}
	return m, nil
}

func connRequest(cmdType int, cmd string, seqNo uint32, msgID, module string, data []byte) []byte {
	return encodeConnMsg(cmdType, cmd, seqNo, msgID, module, data)
}

// encodeBizMsg 将已编码的业务 protobuf 包装为可直接发送的 ConnMsg
// （head.cmd = method, head.module = service，对齐 conn-codec.ts
// buildBusinessConnMsg）。
func encodeBizMsg(method, reqID string, body []byte) []byte {
	return connRequest(cmdTypeRequest, method, nextSeqNo(), reqID, bizPkg, body)
}

// ---- AuthBind / Ping / PushAck ----

// encodeAuthBind 编码 auth-bind 请求 ConnMsg。
//
//	AuthBindReq: 1 biz_id, 2 auth_info (AuthInfo{1 uid, 2 source, 3 token}),
//	  3 device_info (DeviceInfo{1 app_version, 2 app_operation_system, 10 instance_id, 24 bot_version}),
//	  5 env_name
func encodeAuthBind(bizID, uid, source, token, msgID, appVersion, operationSystem, botVersion, routeEnv string) []byte {
	var auth pbBuf
	auth.str(1, uid)
	auth.str(2, source)
	auth.str(3, token)

	var dev pbBuf
	dev.strIf(1, appVersion)
	dev.strIf(2, operationSystem)
	dev.str(10, fmt.Sprint(instanceID))
	dev.strIf(24, botVersion)

	var req pbBuf
	req.str(1, bizID)
	req.nested(2, auth.b)
	req.nested(3, dev.b)
	req.strIf(5, routeEnv)

	return connRequest(cmdTypeRequest, cmdAuthBind, nextSeqNo(), msgID, moduleConnAccess, req.b)
}

// authBindRsp 解码 BIND_ACK 业务 payload：
// AuthBindRsp{1 code, 2 message, 3 connect_id}。
func authBindRsp(data []byte) (code uint64, message, connectID string, err error) {
	d, err := parseDict(data)
	if err != nil {
		return 0, "", "", err
	}
	return d.getVarint(1), d.getString(2), d.getString(3), nil
}

// encodePing 编码 ping 请求 ConnMsg（PingReq 为空消息）。
func encodePing(msgID string) []byte {
	return connRequest(cmdTypeRequest, cmdPing, nextSeqNo(), msgID, moduleConnAccess, nil)
}

// encodePushAck 编码 push ACK 回包（回显原 head 的 cmd / msg_id / module）。
func encodePushAck(head *connMsg) []byte {
	return connRequest(cmdTypePushAck, head.cmd, nextSeqNo(), head.msgID, head.module, nil)
}

// ---- 消息体（TIM 格式） ----

// imageInfo 对齐 _IMAGE_INFO_SPEC。
type imageInfo struct {
	Type, Size, Width, Height uint64
	URL                       string
}

// msgContent 对齐 _MSG_CONTENT_SPEC：1 text, 2 uuid, 3 image_format, 4 data,
// 5 desc, 6 ext, 7 sound, 8 image_info_array (repeated), 9 index, 10 url,
// 11 file_size, 12 file_name。
type msgContent struct {
	Text        string
	UUID        string
	Data        string
	Desc        string
	Ext         string
	Sound       string
	URL         string
	FileName    string
	ImageFormat uint64
	Index       uint64
	FileSize    uint64
	ImageInfo   []imageInfo
}

func (c *msgContent) empty() bool {
	return c.Text == "" && c.UUID == "" && c.Data == "" && c.Desc == "" && c.Ext == "" &&
		c.Sound == "" && c.URL == "" && c.FileName == "" && c.ImageFormat == 0 &&
		c.Index == 0 && c.FileSize == 0 && len(c.ImageInfo) == 0
}

func encodeImageInfo(info imageInfo) []byte {
	var p pbBuf
	p.varintIf(1, info.Type)
	p.varintIf(2, info.Size)
	p.varintIf(3, info.Width)
	p.varintIf(4, info.Height)
	p.strIf(5, info.URL)
	return p.b
}

func decodeImageInfo(data []byte) imageInfo {
	d, err := parseDict(data)
	if err != nil {
		return imageInfo{}
	}
	return imageInfo{
		Type:   d.getVarint(1),
		Size:   d.getVarint(2),
		Width:  d.getVarint(3),
		Height: d.getVarint(4),
		URL:    d.getString(5),
	}
}

func encodeMsgContent(c *msgContent) []byte {
	var p pbBuf
	p.strIf(1, c.Text)
	p.strIf(2, c.UUID)
	p.strIf(4, c.Data)
	p.strIf(5, c.Desc)
	p.strIf(6, c.Ext)
	p.strIf(7, c.Sound)
	p.strIf(10, c.URL)
	p.strIf(12, c.FileName)
	p.varintIf(3, c.ImageFormat)
	p.varintIf(9, c.Index)
	p.varintIf(11, c.FileSize)
	for _, img := range c.ImageInfo {
		p.nested(8, encodeImageInfo(img))
	}
	return p.b
}

func decodeMsgContent(data []byte) *msgContent {
	d, err := parseDict(data)
	if err != nil {
		return &msgContent{}
	}
	c := &msgContent{
		Text:        d.getString(1),
		UUID:        d.getString(2),
		ImageFormat: d.getVarint(3),
		Data:        d.getString(4),
		Desc:        d.getString(5),
		Ext:         d.getString(6),
		Sound:       d.getString(7),
		Index:       d.getVarint(9),
		URL:         d.getString(10),
		FileSize:    d.getVarint(11),
		FileName:    d.getString(12),
	}
	for _, b := range d.repeatedBytes(8) {
		c.ImageInfo = append(c.ImageInfo, decodeImageInfo(b))
	}
	return c
}

// msgBodyElem 对齐 MsgBodyElement：1 msg_type (e.g. "TIMTextElem"), 2 msg_content。
type msgBodyElem struct {
	MsgType   string
	MsgContent msgContent
}

func encodeMsgBodyElem(el msgBodyElem) []byte {
	var p pbBuf
	p.strIf(1, el.MsgType)
	if !el.MsgContent.empty() {
		p.nested(2, encodeMsgContent(&el.MsgContent))
	}
	return p.b
}

func decodeMsgBodyElem(data []byte) msgBodyElem {
	d, err := parseDict(data)
	if err != nil {
		return msgBodyElem{}
	}
	el := msgBodyElem{MsgType: d.getString(1)}
	if cb := d.getBytes(2); cb != nil {
		el.MsgContent = *decodeMsgContent(cb)
	}
	return el
}

// textElem 构造一条 TIMTextElem（对齐 python _text_elem）。
func textElem(text string) msgBodyElem {
	return msgBodyElem{MsgType: "TIMTextElem", MsgContent: msgContent{Text: text}}
}

// ---- 出站消息编码 ----

// encodeSendC2CMessage 编码 SendC2CMessageReq → 完整 ConnMsg。
//
//	SendC2CMessageReq: 1 msg_id, 2 to_account, 3 from_account, 4 msg_random,
//	  5 msg_body (repeated), 6 group_code, 7 msg_seq, 8 trace_id
//
// req_id 即 msg_id（对齐 python send_c2c_msg_body 的 "c2c_<seq>"）。
// group_code 用于"群内私聊"场景。
func encodeSendC2CMessage(toAccount string, body []msgBodyElem, fromAccount, reqID, groupCode string) []byte {
	var p pbBuf
	p.strIf(1, reqID)
	p.str(2, toAccount)
	p.strIf(3, fromAccount)
	for _, el := range body {
		p.nested(5, encodeMsgBodyElem(el))
	}
	p.strIf(6, groupCode)
	return encodeBizMsg("send_c2c_message", reqID, p.b)
}

// encodeSendGroupMessage 编码 SendGroupMessageReq → 完整 ConnMsg。
//
//	SendGroupMessageReq: 1 msg_id, 2 group_code, 3 from_account, 4 to_account,
//	  5 random, 6 msg_body (repeated), 7 ref_msg_id (引用消息), 8 msg_seq, 9 trace_id
//
// to_account 通常为空。
func encodeSendGroupMessage(groupCode string, body []msgBodyElem, fromAccount, reqID, refMsgID string) []byte {
	var p pbBuf
	p.strIf(1, reqID)
	p.str(2, groupCode)
	p.strIf(3, fromAccount)
	p.strIf(7, refMsgID)
	for _, el := range body {
		p.nested(6, encodeMsgBodyElem(el))
	}
	return encodeBizMsg("send_group_message", reqID, p.b)
}

// ---- 入站消息解码 ----

// inboundPush 对齐 InboundMessagePush 字段表（yuanbao_proto.py
// _INBOUND_PUSH_SPEC）。
type inboundPush struct {
	CallbackCommand      string
	FromAccount          string
	ToAccount            string
	SenderNickname       string
	GroupID              string
	GroupCode            string
	GroupName            string
	MsgSeq               uint64
	MsgRandom            uint64
	MsgTime              uint64
	MsgKey               string
	MsgID                string
	MsgBody              []msgBodyElem
	CloudCustomData      string
	EventTime            uint64
	BotOwnerID           string
	ClawMsgType          uint64
	PrivateFromGroupCode string
	TraceID              string
}

// decodeInboundPushProto 解析 protobuf 格式的 InboundMessagePush biz payload。
func decodeInboundPushProto(data []byte) (*inboundPush, error) {
	d, err := parseDict(data)
	if err != nil {
		return nil, err
	}
	push := &inboundPush{
		CallbackCommand:      d.getString(1),
		FromAccount:          d.getString(2),
		ToAccount:            d.getString(3),
		SenderNickname:       d.getString(4),
		GroupID:              d.getString(5),
		GroupCode:            d.getString(6),
		GroupName:            d.getString(7),
		MsgSeq:               d.getVarint(8),
		MsgRandom:            d.getVarint(9),
		MsgTime:              d.getVarint(10),
		MsgKey:               d.getString(11),
		MsgID:                d.getString(12),
		CloudCustomData:      d.getString(14),
		EventTime:            d.getVarint(15),
		BotOwnerID:           d.getString(16),
		ClawMsgType:          d.getVarint(18),
		PrivateFromGroupCode: d.getString(19),
	}
	for _, b := range d.repeatedBytes(13) {
		push.MsgBody = append(push.MsgBody, decodeMsgBodyElem(b))
	}
	// field 20 trace_id: LogInfoExt{1 trace_id}
	if tb := d.getBytes(20); len(tb) > 0 {
		if td, err := parseDict(tb); err == nil {
			push.TraceID = td.getString(1)
		}
	}
	return push, nil
}

// decodeInboundFrame 解码入站 biz payload：JSON 优先，其次 protobuf
// （对齐 python DecodeMiddleware._decode_single 的双格式支持）。
// 无有效内容时返回 nil。
func decodeInboundFrame(data []byte) *inboundPush {
	push := parseJSONPush(data)
	if push != nil {
		return push
	}
	push, err := decodeInboundPushProto(data)
	if err != nil {
		return nil
	}
	return push
}

// validPush 判断是否为可分发的消息（对齐 python parse_json_push 的有效性
// 判断：无 from_account、无 msg_body 且无 callback_command 的帧无效）。
func (p *inboundPush) validPush() bool {
	return p.FromAccount != "" || len(p.MsgBody) > 0 || p.CallbackCommand != ""
}

// ---- JSON push 解析（对齐 python DecodeMiddleware.parse_json_push）----

// parseJSONPush 解析 JSON 格式的 InboundMessagePush。data 必须是 JSON 对象，
// 否则返回 nil（交由 protobuf 路径处理）。同时兼容 Tencent IM PascalCase
// （From_Account / GroupId / MsgBody / ...）与内部 snake_case 键。
func parseJSONPush(data []byte) *inboundPush {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil
	}
	push := &inboundPush{
		CallbackCommand: jsonStr(raw, "callback_command"),
		FromAccount:     firstNonEmpty(jsonStr(raw, "from_account"), jsonStr(raw, "From_Account")),
		ToAccount:       firstNonEmpty(jsonStr(raw, "to_account"), jsonStr(raw, "To_Account")),
		SenderNickname:  firstNonEmpty(jsonStr(raw, "sender_nickname"), jsonStr(raw, "nick_name")),
		GroupCode: firstNonEmpty(jsonStr(raw, "group_code"), jsonStr(raw, "GroupId"),
			jsonStr(raw, "group_id")),
		GroupName:       jsonStr(raw, "group_name"),
		MsgSeq:          firstNonZero(jsonUint(raw, "msg_seq"), jsonUint(raw, "MsgSeq")),
		MsgID:           firstNonEmpty(jsonStr(raw, "msg_id"), jsonStr(raw, "msg_key"), jsonStr(raw, "MsgKey")),
		CloudCustomData: firstNonEmpty(jsonStr(raw, "cloud_custom_data"), jsonStr(raw, "CloudCustomData")),
		BotOwnerID:      firstNonEmpty(jsonStr(raw, "bot_owner_id"), jsonStr(raw, "botOwnerId")),
	}
	for _, elem := range jsonMsgBody(raw) {
		push.MsgBody = append(push.MsgBody, elem)
	}
	// 撤回回调可能既无 from_account 也无 msg_body。
	if !push.validPush() {
		return nil
	}
	return push
}

func jsonStr(m map[string]json.RawMessage, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

func jsonUint(m map[string]json.RawMessage, key string) uint64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	var n uint64
	if err := json.Unmarshal(v, &n); err != nil {
		return 0
	}
	return n
}

// jsonMsgBody 提取 msg_body/MsgBody 并归一化为 msgBodyElem
// （对齐 python convert_json_msg_body：PascalCase 或 snake_case 键；
// msg_content 为字符串时先尝试 JSON 解析，失败则视为 text）。
func jsonMsgBody(m map[string]json.RawMessage) []msgBodyElem {
	rawBody, ok := m["msg_body"]
	if !ok {
		rawBody, ok = m["MsgBody"]
	}
	if !ok {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &items); err != nil {
		return nil
	}
	var out []msgBodyElem
	for _, item := range items {
		msgType := firstNonEmpty(jsonStr(item, "msg_type"), jsonStr(item, "MsgType"))
		contentRaw, hasContent := item["msg_content"]
		if !hasContent {
			contentRaw, hasContent = item["MsgContent"]
		}
		var content msgContent
		if hasContent {
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(contentRaw, &probe); err == nil && probe != nil {
				content = msgContent{
					Text: firstNonEmpty(jsonStr(probe, "text"), jsonStr(probe, "Text")),
					Data: firstNonEmpty(jsonStr(probe, "data"), jsonStr(probe, "Data")),
				}
			} else {
				var s string
				if err := json.Unmarshal(contentRaw, &s); err == nil {
					// 字符串内容：先尝试 JSON 对象，失败按纯文本（对齐 python）。
					var obj map[string]any
					if json.Unmarshal([]byte(s), &obj) == nil && obj != nil {
						if t, ok := obj["text"].(string); ok {
							content.Text = t
						}
					} else {
						content.Text = s
					}
				} else {
					content.Text = s
				}
			}
		}
		out = append(out, msgBodyElem{MsgType: msgType, MsgContent: content})
	}
	return out
}
