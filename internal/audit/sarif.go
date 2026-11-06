package audit

import (
	"encoding/json"
	"io"
	"path"
	"slices"
)

// WriteSARIF writes findings as a SARIF 2.1.0 log, the format GitHub code
// scanning and most IDEs read. Each repo becomes its own run so file paths
// stay relative to that repo. Ignored findings are left out.
func WriteSARIF(w io.Writer, toolVersion string, fs []Finding) error {
	type region struct {
		StartLine int `json:"startLine,omitempty"`
	}
	type artifact struct {
		URI       string `json:"uri"`
		URIBaseID string `json:"uriBaseId,omitempty"`
	}
	type physical struct {
		ArtifactLocation artifact `json:"artifactLocation"`
		Region           *region  `json:"region,omitempty"`
	}
	type locationT struct {
		PhysicalLocation physical `json:"physicalLocation"`
	}
	type message struct {
		Text string `json:"text"`
	}
	type result struct {
		RuleID              string            `json:"ruleId"`
		Level               string            `json:"level"`
		Message             message           `json:"message"`
		Locations           []locationT       `json:"locations"`
		PartialFingerprints map[string]string `json:"partialFingerprints"`
		Properties          map[string]any    `json:"properties"`
	}
	type rule struct {
		ID               string  `json:"id"`
		ShortDescription message `json:"shortDescription"`
	}
	type driver struct {
		Name           string `json:"name"`
		InformationURI string `json:"informationUri"`
		Version        string `json:"version"`
		Rules          []rule `json:"rules"`
	}
	type run struct {
		Tool struct {
			Driver driver `json:"driver"`
		} `json:"tool"`
		OriginalURIBaseIDs map[string]artifact `json:"originalUriBaseIds"`
		Results            []result            `json:"results"`
	}

	byRepo := map[string][]Finding{}
	var repos []string
	for _, f := range fs {
		if f.Status == StatusIgnored {
			continue
		}
		if _, ok := byRepo[f.RepoPath]; !ok {
			repos = append(repos, f.RepoPath)
		}
		byRepo[f.RepoPath] = append(byRepo[f.RepoPath], f)
	}
	slices.Sort(repos)

	runs := []run{}
	for _, repo := range repos {
		var r run
		r.Tool.Driver = driver{Name: "ChitraGit KodeKavach", InformationURI: "https://github.com/TTox-Tech/ChitraGit", Version: toolVersion}
		r.OriginalURIBaseIDs = map[string]artifact{"REPO": {URI: path.Clean(repo) + "/"}}
		seen := map[string]bool{}
		for _, f := range byRepo[repo] {
			if !seen[f.Rule] {
				seen[f.Rule] = true
				r.Tool.Driver.Rules = append(r.Tool.Driver.Rules, rule{ID: f.Rule, ShortDescription: message{f.Title}})
			}
			loc := physical{ArtifactLocation: artifact{URI: f.File, URIBaseID: "REPO"}}
			if f.Line > 0 {
				loc.Region = &region{StartLine: f.Line}
			}
			r.Results = append(r.Results, result{
				RuleID:              f.Rule,
				Level:               sarifLevel(f.Severity),
				Message:             message{f.Title + " (" + f.Preview + "). " + f.Hint},
				Locations:           []locationT{{loc}},
				PartialFingerprints: map[string]string{"chitragitFinding/v1": f.ID},
				Properties: map[string]any{
					"severity": f.Severity, "confidence": f.Confidence, "check": f.Check,
					"commit": f.Commit, "occurrences": f.Occurrences,
				},
			})
		}
		runs = append(runs, r)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs":    runs,
	})
}

func sarifLevel(s Severity) string {
	switch s {
	case SevCritical, SevHigh:
		return "error"
	case SevMedium:
		return "warning"
	}
	return "note"
}
