package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/skill"
)

// 验证技能链路:发现 → 索引注入 → 预处理读取 → skill_manage 工具全动作
func TestSkillCapabilityEndToEnd(t *testing.T) {
	// 1. 真实用户技能目录:发现
	home, _ := os.UserHomeDir()
	userSkills := filepath.Join(home, ".covo-agent", "skills")
	mgr := NewSkillManager(userSkills, NewSkillUsageTracker(userSkills))
	skills, _ := mgr.List()
	t.Logf("用户技能目录发现 %d 个技能", len(skills))
	for i, s := range skills {
		if i < 5 {
			t.Logf("  - %s (dir: %s)", s.Name, s.Dir)
		}
	}

	// 2. 索引注入渲染(covo 侧构造 skill.Skill,与 internal/agent/agent.go:1158 同构)
	var covonautSkills []skill.Skill
	for _, s := range skills {
		covonautSkills = append(covonautSkills, skill.Skill{
			Name:        s.Name,
			Description: s.Description,
			FilePath:    filepath.Join(s.Dir, "SKILL.md"),
		})
	}
	idx := skill.Index(covonautSkills)
	if len(covonautSkills) > 0 && !strings.Contains(idx, "<available_skills>") {
		t.Fatal("索引注入未渲染 available_skills")
	}
	if strings.Contains(idx, "<path>") {
		t.Log("索引含 <path>:read 兜底可用")
	}

	// 3. 预处理读取(/skill: 通道)
	if len(skills) > 0 {
		if _, err := mgr.ReadPreprocessed(skills[0].Name, PreprocessConfig{SessionID: "verify"}); err != nil {
			t.Fatalf("ReadPreprocessed(%s): %v", skills[0].Name, err)
		}
		t.Logf("ReadPreprocessed(%s) OK", skills[0].Name)
	}

	// 4. skill_manage 工具全动作(临时目录,不碰真实技能)
	tmp := t.TempDir()
	skMgr := NewSkillManager(tmp, NewSkillUsageTracker(tmp))
	usage := NewSkillUsageTracker(tmp)
	ext := NewEvolutionExtension(nil, skMgr, nil, usage, func() string { return "verify-session" })
	manage := ext.buildSkillManageTool()
	call := func(params map[string]any) {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := manage.Func(context.Background(), raw); err != nil {
			t.Fatalf("action %v: %v", params["action"], err)
		}
	}
	call(map[string]any{"action": "create", "name": "verify-skill", "description": "verify", "body": "session=${COVO_SESSION_ID}\n# steps\n1. do it"})
	if _, err := os.Stat(filepath.Join(tmp, "verify-skill", "SKILL.md")); err != nil {
		t.Fatalf("create 未落盘: %v", err)
	}
	call(map[string]any{"action": "read", "name": "verify-skill"})
	call(map[string]any{"action": "patch", "name": "verify-skill", "find_str": "do it", "replace_str": "done"})
	call(map[string]any{"action": "improve", "name": "verify-skill", "improvement_summary": "test"})
	skillTool := ext.buildSkillTool()
	loadRaw, _ := json.Marshal(map[string]any{"name": "verify-skill"})
	loadRes, err := skillTool.Func(context.Background(), loadRaw)
	if err != nil {
		t.Fatalf("skill 加载: %v", err)
	}
	if m, ok := loadRes.(map[string]any); !ok {
		t.Fatalf("skill 加载返回类型异常: %v", loadRes)
	} else {
		content := m["content"].(string)
		if !strings.Contains(content, "done") {
			t.Fatalf("skill 加载内容不含预期正文: %v", content)
		}
		if !strings.Contains(content, "session=verify-session") {
			t.Fatalf("SessionID 未注入模板: %q", content)
		}
	}
	if _, err := skillTool.Func(context.Background(), []byte(`{"name":"nope"}`)); err == nil {
		t.Fatal("未知技能应报错")
	}
	t.Log("skill 工具加载 + 未知名报错 通过")

	call(map[string]any{"action": "delete", "name": "verify-skill"})
	if _, err := os.Stat(filepath.Join(tmp, "verify-skill")); !os.IsNotExist(err) {
		t.Log("delete 归档处理:", err)
	}
	t.Log("skill_manage create/read/patch/improve/delete 全部通过")
}
