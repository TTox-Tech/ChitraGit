package engine

// Re-exports for KodeKavach: findings, rules packs and the OSV database,
// so the UI packages reach internal/audit only through engine.

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/TTox-Tech/chitragit/internal/audit"
)

type (
	EcosystemStatus = audit.EcosystemStatus
	Finding         = audit.Finding
	RulesPack       = audit.RulesPack
	RulesUpdate     = audit.RulesUpdate
	AuditTool       = audit.Tool
	VulnDBProgress  = audit.UpdateProgress
	VulnDBUpdate    = audit.UpdateResult
	VulnDB          = audit.VulnDB
)

const (
	CheckActions       = audit.CheckActions
	CheckAgents        = audit.CheckAgents
	CheckDeps          = audit.CheckDeps
	ConfidenceLow      = audit.ConfidenceLow
	HistoryFull        = audit.HistoryFull
	HistoryHead        = audit.HistoryHead
	HistoryIncremental = audit.HistoryIncremental
	OSVBaseURL         = audit.OSVBaseURL
	RulesAsset         = audit.RulesAsset
	RulesSigAsset      = audit.RulesSigAsset
	SevHigh            = audit.SevHigh
	FindingFixed       = audit.StatusFixed
	FindingIgnored     = audit.StatusIgnored
	ToolAuto           = audit.ToolAuto
	ToolSkillSpector   = audit.ToolSkillSpector
	ToolZizmor         = audit.ToolZizmor
)

var (
	AllAuditChecks  = audit.AllChecks
	Ecosystems      = audit.Ecosystems
	ErrBadSignature = audit.ErrBadSignature
	ErrNoRulesKey   = audit.ErrNoRulesKey
	Severities      = audit.Severities
)

func SeverityAtLeast(fs []audit.Finding, min audit.Severity) bool { return audit.AtLeast(fs, min) }

func FindAuditTool(ctx context.Context, name string, mode string) (*audit.Tool, error) {
	return audit.FindTool(ctx, name, mode)
}

func InstallRules(dir string, pack []byte, sig []byte) (*audit.RulesUpdate, error) {
	return audit.InstallRules(dir, pack, sig)
}

func LoadRules(dir string) (pack *audit.RulesPack, warn error) { return audit.LoadRules(dir) }

func MarkRulesChecked(dir string) { audit.MarkRulesChecked(dir) }

func OpenVulnDB(dir string) (*audit.VulnDB, error) { return audit.OpenVulnDB(dir) }

func ParseSeverity(s string) (audit.Severity, bool) { return audit.ParseSeverity(s) }

func RulesDir(cacheDir string) string { return audit.RulesDir(cacheDir) }

func RulesLastChecked(dir string) time.Time { return audit.RulesLastChecked(dir) }

func RulesUpdateDue(dir string, every time.Duration) bool { return audit.RulesUpdateDue(dir, every) }

func SummariseFindings(fs []audit.Finding) audit.Summary { return audit.Summarise(fs) }

func UpdateRules(ctx context.Context, dir string, baseURL string, client *http.Client) (*audit.RulesUpdate, error) {
	return audit.UpdateRules(ctx, dir, baseURL, client)
}

func WriteSARIF(w io.Writer, toolVersion string, fs []audit.Finding) error {
	return audit.WriteSARIF(w, toolVersion, fs)
}

// SignedRulesUpdates reports whether this build can verify rules packs.
func SignedRulesUpdates() bool { return audit.RulesPublicKey != "" }
