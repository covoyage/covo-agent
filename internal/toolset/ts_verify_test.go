package toolset

import "testing"

func TestVerifySkillToolset(t *testing.T) {
	resolved, err := ResolveToolsets([]string{"full"})
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, r := range resolved {
		set[r] = true
	}
	for _, name := range []string{"skill", "skill_manage", "skill_bundle", "skill_config", "skill_script", "skill_workshop", "memory"} {
		status := "✗ 排除"
		if set[name] {
			status = "✓ 保留"
		}
		t.Logf("%-16s %s", name, status)
	}
	t.Logf("full 解析后共 %d 个工具名", len(resolved))
}
