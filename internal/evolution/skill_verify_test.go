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

	// 4. skill_manage 写入走提案,不直接改 live SKILL.md
	tmp := t.TempDir()
	skMgr := NewSkillManager(tmp, NewSkillUsageTracker(tmp))
	usage := NewSkillUsageTracker(tmp)
	store := NewWorkshopStore(tmp)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	ext := NewEvolutionExtension(nil, skMgr, nil, usage, func() string { return "verify-session" })
	ext.SetWorkshopStore(store)
	manage := ext.buildSkillManageTool()
	call := func(params map[string]any) any {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out, err := manage.Func(context.Background(), raw)
		if err != nil {
			t.Fatalf("action %v: %v", params["action"], err)
		}
		return out
	}
	created := call(map[string]any{"action": "create", "name": "verify-new", "description": "verify", "body": "session=${COVO_SESSION_ID}\n# steps\n1. do it"})
	if m, ok := created.(map[string]any); !ok || m["status"] != "proposed" {
		t.Fatalf("create should propose, got %#v", created)
	}
	if _, err := os.Stat(filepath.Join(tmp, "verify-new", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("create must not write live SKILL.md")
	}

	if _, err := skMgr.Create("verify-skill", "verify", "session=${COVO_SESSION_ID}\n# steps\n1. do it"); err != nil {
		t.Fatalf("seed live skill: %v", err)
	}
	call(map[string]any{"action": "read", "name": "verify-skill"})
	patched := call(map[string]any{"action": "patch", "name": "verify-skill", "find_str": "do it", "replace_str": "done"})
	if m, ok := patched.(map[string]any); !ok || m["status"] != "proposed" {
		t.Fatalf("patch should propose, got %#v", patched)
	}
	live, err := skMgr.Read("verify-skill")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(live, "done") {
		t.Fatal("patch must not mutate live skill")
	}
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
		if !strings.Contains(content, "do it") {
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

	deleted := call(map[string]any{"action": "delete", "name": "verify-other"})
	if m, ok := deleted.(map[string]any); !ok || m["status"] != "proposed" {
		t.Fatalf("delete should propose, got %#v", deleted)
	}
	if _, err := os.Stat(filepath.Join(tmp, "verify-skill", "SKILL.md")); err != nil {
		t.Fatalf("delete proposal must leave live skill: %v", err)
	}
	t.Log("skill_manage create/read/patch/improve/delete 全部通过")
}
