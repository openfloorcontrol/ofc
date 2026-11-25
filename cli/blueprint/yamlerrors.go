package blueprint

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// readableYAMLError rewrites the errors yaml.v3 reports while decoding
// into v in the blueprint's terms: an unknown field names the section it
// is in and the closest known field, a value of the wrong kind says what
// was expected. Other errors are returned as they are.
func readableYAMLError(err error, v any) error {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err
	}
	sections := yamlSections(reflect.TypeOf(v))
	lines := make([]string, len(typeErr.Errors))
	for i, msg := range typeErr.Errors {
		lines[i] = readableLine(msg, sections)
	}
	return errors.New(strings.Join(lines, "\n"))
}

var (
	// line 4: field promt not found in type blueprint.Agent
	unknownFieldRe = regexp.MustCompile("^(line \\d+): field (\\S+) not found in type (\\S+)$")
	// line 4: cannot unmarshal !!str `tasks` into []string
	wrongKindRe = regexp.MustCompile("^(line \\d+): cannot unmarshal !!(\\w+) `(.*)` into (\\S+)$")
)

func readableLine(msg string, sections map[string]section) string {
	if m := unknownFieldRe.FindStringSubmatch(msg); m != nil {
		s, ok := sections[m[3]]
		if !ok {
			return msg
		}
		out := fmt.Sprintf("%s: %s has no field %q", m[1], s.name, m[2])
		if best := closest(m[2], s.fields); best != "" {
			out += fmt.Sprintf(" — did you mean %q?", best)
		}
		return out
	}
	if m := wrongKindRe.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("%s: expected %s, got %s", m[1], expectedKind(m[4]), givenKind(m[2], m[3]))
	}
	return msg
}

// section is a struct of the blueprint as its YAML shows it: what to call
// it and its fields.
type section struct {
	name   string
	fields []string
}

// sectionNames are what errors call the blueprint's structs.
var sectionNames = map[string]string{
	"Blueprint":    "the blueprint",
	"Agent":        "an agent",
	"FurnitureDef": "a furniture entry",
	"ModelDef":     "a model",
	"Defaults":     "defaults",
	"FloorControl": "floor_control",
	"OAuthConfig":  "oauth",
}

// yamlSections collects the structs reachable from t, by the name yaml.v3
// gives them in errors (e.g. "blueprint.Agent").
func yamlSections(t reflect.Type) map[string]section {
	out := map[string]section{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || out[t.String()].name != "" {
			return
		}
		name := sectionNames[t.Name()]
		if name == "" {
			name = t.Name()
		}
		s := section{name: name}
		out[t.String()] = s
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if tag == "-" || !f.IsExported() {
				continue
			}
			if tag == "" {
				tag = strings.ToLower(f.Name)
			}
			s.fields = append(s.fields, tag)
			walk(f.Type)
		}
		sort.Strings(s.fields)
		out[t.String()] = s
	}
	walk(t)
	return out
}

// closest returns the field nearest to name, if it is near enough to be
// a likely typo: at most two edits, or one for short names.
func closest(name string, fields []string) string {
	best, bestDist := "", 3
	if len(name) <= 4 {
		bestDist = 2
	}
	for _, f := range fields {
		if d := editDistance(name, f); d < bestDist {
			best, bestDist = f, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// expectedKind describes a Go type as the YAML it takes.
func expectedKind(goType string) string {
	switch {
	case strings.HasPrefix(goType, "[]"):
		return "a list"
	case strings.HasPrefix(goType, "map["):
		return "a mapping (key: value)"
	case goType == "string":
		return "a text"
	case goType == "bool" || goType == "*bool":
		return "true or false"
	case strings.Contains(goType, "int"), strings.Contains(goType, "float"):
		return "a number"
	case strings.Contains(goType, "Duration"):
		return "a duration like 30s or 5m"
	default:
		return "a mapping (key: value)"
	}
}

// givenKind describes a YAML value by its tag and text.
func givenKind(tag, value string) string {
	switch tag {
	case "str":
		return fmt.Sprintf("the text %q", value)
	case "int", "float":
		return "the number " + value
	case "bool":
		return value
	case "seq":
		return "a list"
	case "map":
		return "a mapping"
	default:
		return value
	}
}
