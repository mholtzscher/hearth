package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"gopkg.in/yaml.v3"
)

const automationSkillName = "hearth-automations"

//go:embed skills/hearth-automations/SKILL.md
var automationSkillContent string

// embeddedSkills contains the validated, inline-only built-in catalog.
type embeddedSkills struct {
	automation skill.Skill
}

var _ skill.Backend = embeddedSkills{}

func newEmbeddedSkills() (embeddedSkills, error) {
	return newEmbeddedSkillsFromContent(automationSkillContent)
}

func newEmbeddedSkillsFromContent(content string) (embeddedSkills, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return embeddedSkills{}, fmt.Errorf("skill frontmatter opening delimiter is missing")
	}
	frontmatter, body, found := strings.Cut(strings.TrimPrefix(content, "---\n"), "\n---\n")
	if !found {
		return embeddedSkills{}, fmt.Errorf("skill frontmatter closing delimiter is missing")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &document); err != nil {
		return embeddedSkills{}, fmt.Errorf("decode skill frontmatter: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return embeddedSkills{}, fmt.Errorf("skill frontmatter must be a mapping")
	}
	fields := document.Content[0].Content
	for index := 0; index < len(fields); index += 2 {
		if fields[index+1].Kind != yaml.ScalarNode || fields[index+1].Tag != "!!str" {
			return embeddedSkills{}, fmt.Errorf("skill frontmatter field %q must be a string", fields[index].Value)
		}
	}
	decoder := yaml.NewDecoder(strings.NewReader(frontmatter))
	decoder.KnownFields(true)
	var metadata skill.FrontMatter
	if err := decoder.Decode(&metadata); err != nil {
		return embeddedSkills{}, fmt.Errorf("decode skill frontmatter: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return embeddedSkills{}, fmt.Errorf("skill frontmatter must contain one YAML document")
	}
	if metadata.Name != automationSkillName {
		return embeddedSkills{}, fmt.Errorf("skill name must be %q", automationSkillName)
	}
	if strings.TrimSpace(metadata.Description) == "" || strings.TrimSpace(body) == "" {
		return embeddedSkills{}, fmt.Errorf("skill description and body must be nonempty")
	}
	if metadata.Context != "" || metadata.Agent != "" || metadata.Model != "" {
		return embeddedSkills{}, fmt.Errorf("skill must be inline without agent or model overrides")
	}
	return embeddedSkills{automation: skill.Skill{FrontMatter: metadata, Content: body}}, nil
}

func (s embeddedSkills) List(_ context.Context) ([]skill.FrontMatter, error) {
	return []skill.FrontMatter{s.automation.FrontMatter}, nil
}

func (s embeddedSkills) Get(_ context.Context, name string) (skill.Skill, error) {
	if name != s.automation.Name {
		return skill.Skill{}, fmt.Errorf("unknown skill %q", name)
	}
	return s.automation, nil
}
