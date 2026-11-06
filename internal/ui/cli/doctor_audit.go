package cli

import (
	"context"

	"github.com/TTox-Tech/chitragit/internal/audit"
)

// auditChecks reports KodeKavach's rules pack and the optional external
// scanners, so users can see what `chitragit audit` will run.
func (a *app) auditChecks(ctx context.Context, add func(name string, st checkStatus, detail, hint string)) {
	if dir, err := rulesDir(); err == nil {
		pack, warn := audit.LoadRules(dir)
		switch {
		case pack == nil:
			add("KodeKavach rules", statusFail, warn.Error(), "")
		case warn != nil:
			add("KodeKavach rules", statusWarn, pack.Version+" ("+pack.Source+")\n"+warn.Error(), "run `chitragit audit rules update`")
		default:
			add("KodeKavach rules", statusOK, pack.Version+" ("+pack.Source+")", "")
		}
	}
	for _, t := range []struct{ name, label, hint string }{
		{audit.ToolZizmor, "zizmor", "optional, for a deeper workflow audit: https://docs.zizmor.sh"},
		{audit.ToolSkillSpector, "SkillSpector", "optional, for a deeper agent skill audit: https://github.com/NVIDIA/skillspector"},
	} {
		tool, _ := audit.FindTool(ctx, t.name, audit.ToolAuto)
		if tool == nil {
			add(t.label, statusOK, "not installed (optional extra, off by default)", t.hint)
			continue
		}
		detail := tool.Path
		if tool.Version != "" {
			detail = tool.Version + " (" + tool.Path + ")"
		}
		add(t.label, statusOK, detail, "")
	}
}
