package skillclient

import (
	"os"
	"path/filepath"
	"sort"
)

var agentSkillRoots = map[string][]string{
	"codex":          {".codex/skills"},
	"claude-code":    {".claude/skills"},
	"cursor":         {".cursor/skills"},
	"gemini-cli":     {".gemini/skills"},
	"github-copilot": {".copilot/skills"},
	"opencode":       {".config/opencode/skills"},
	"goose":          {".config/goose/skills"},
	"aider":          {".aider/skills"},
	"amp":            {".config/amp/skills"},
}

func Destination(home, agent string) (string, error) {
	roots := agentSkillRoots[agent]
	if len(roots) == 0 {
		return "", ErrUnsupportedAgent
	}
	return filepath.Join(home, filepath.FromSlash(roots[0]), SkillName), nil
}

func DetectInstalledAgents(home, current string) []string {
	found := make(map[string]struct{})
	for agent, roots := range agentSkillRoots {
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
