package skillclient

import (
	"os"
	"path/filepath"
	"sort"
)

var agentSkillRoots = map[string][]string{
	"agents":         {".agents/skills"},
	"codex":          {".codex/skills"},
	"claude-code":    {".claude/skills"},
	"cursor":         {".cursor/skills"},
	"gemini-cli":     {".gemini/skills"},
	"antigravity":    {".gemini/config/skills"},
	"github-copilot": {".copilot/skills"},
	"opencode":       {".config/opencode/skills"},
	"goose":          {".config/goose/skills"},
	"aider":          {".aider/skills"},
	"amp":            {".config/amp/skills"},
}

// fallbackSkillAgents are detected agents that support Agent Skills but do not
// publish a product-specific user skill directory. They share the portable
// ~/.agents/skills location.
var fallbackSkillAgents = map[string]struct{}{
	"default":   {},
	"devin":     {},
	"hermes":    {},
	"openhands": {},
}

func Destination(home, agent string) (string, error) {
	agent = canonicalSkillAgent(agent)
	roots := agentSkillRoots[agent]
	if len(roots) == 0 {
		return "", ErrUnsupportedAgent
	}
	return filepath.Join(home, filepath.FromSlash(roots[0]), SkillName), nil
}

func canonicalSkillAgent(agent string) string {
	if _, fallback := fallbackSkillAgents[agent]; fallback {
		return "agents"
	}
	return agent
}

func KnownDestinations(home string) map[string]string {
	destinations := make(map[string]string, len(agentSkillRoots))
	for agent := range agentSkillRoots {
		destination, _ := Destination(home, agent)
		destinations[agent] = destination
	}
	return destinations
}

func DetectInstalledAgents(home, current string) []string {
	found := make(map[string]struct{})
	for agent, roots := range agentSkillRoots {
		if agent == "agents" {
			continue
		}
		if agent == current {
			continue
		}
		for _, root := range roots {
			info, err := os.Stat(filepath.Join(home, filepath.FromSlash(root), ".."))
			if err == nil && info.IsDir() {
				found[agent] = struct{}{}
				break
			}
		}
	}
	output := make([]string, 0, len(found))
	for agent := range found {
		output = append(output, agent)
	}
	sort.Strings(output)
	return output
}
