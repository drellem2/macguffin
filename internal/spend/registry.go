package spend

import (
	"encoding/json"
	"os/exec"
)

// AgentRegistryReader returns a snapshot of pogod's runtime agent registry as
// a map from agent name (e.g. "pc-e4e7", "architect") to the mg WorkItemID
// the agent is assigned to. Empty values are skipped. Returns nil/empty when
// the registry is unreachable — callers fall back to events.jsonl
// interval-join attribution in that case.
type AgentRegistryReader func() map[string]string

// readAgentRegistry is the default AgentRegistryReader. It shells out to
// `pogo agent list --json` and extracts each agent's WorkItemID. pogod keeps
// the registry in-memory (no file equivalent), so the CLI is the supported
// integration point — same pattern as flow.countRunningPolecats.
//
// Failures (pogo not installed, daemon not running, malformed JSON) are
// silent: the aggregator already attributes from events.jsonl and falls
// through to overhead buckets when both signals are missing.
func readAgentRegistry() map[string]string {
	cmd := exec.Command("pogo", "agent", "list", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var agents []struct {
		Name       string `json:"name"`
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.Unmarshal(out, &agents); err != nil {
		return nil
	}
	m := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.WorkItemID != "" {
			m[a.Name] = a.WorkItemID
		}
	}
	return m
}
