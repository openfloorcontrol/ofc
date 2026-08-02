package blueprint

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Environment variable expansion is a property of blueprint *loading*, not of
// individual fields: every string in the blueprint is expanded by one pass,
// right after unmarshalling. Fields don't opt in — they opt out, with the
// struct tag `env:"-"` (see Agent.Prompt, which is content rather than
// configuration and may legitimately contain a literal '$').
//
// Expansion happens once, at load. The resulting values are baked into the
// Blueprint: a floor started with one environment keeps behaving that way even
// if the environment changes underneath it later.
//
// Syntax follows bash, so there is nothing OFC-specific to learn:
//
//	${VAR}         required — unset is an error; set-but-empty is fine
//	${VAR-def}     use "def" if VAR is unset
//	${VAR:-def}    use "def" if VAR is unset *or* empty
//
// Because a default can always be written explicitly, a bare ${VAR} that isn't
// set is an unambiguous misconfiguration rather than something to warn about
// and continue past.

// missingVar records one unset ${VAR} reference and where it appeared.
type missingVar struct {
	Name     string // the variable name, e.g. "OPENAI_API_KEY"
	Location string // where in the blueprint, e.g. "agents[0].api_key"
}

// MissingEnvError reports every unset variable found in one load, so a
// misconfigured blueprint takes one round-trip to fix instead of one per
// variable.
type MissingEnvError struct {
	Missing []missingVar
}

func (e *MissingEnvError) Error() string {
	var b strings.Builder
	if len(e.Missing) == 1 {
		b.WriteString("unset environment variable:\n")
	} else {
		b.WriteString("unset environment variables:\n")
	}
	for _, m := range e.Missing {
		fmt.Fprintf(&b, "  %s: ${%s}\n", m.Location, m.Name)
	}
	b.WriteString("\nSet the variable, or give it a default: ${" +
		e.Missing[0].Name + ":-some-value}")
	return b.String()
}

// expandString expands ${VAR} references in s, appending any unset variable
// (one without a default) to missing rather than failing immediately.
//
// A reference is the sequence "${" up to its matching "}", and nothing else:
// a bare $VAR or a lone '$' is data. Blueprint strings hold secrets, prices
// and shell snippets, so a literal '$' has to come through untouched.
func expandString(s, location string, missing *[]missingVar) string {
	if !strings.Contains(s, "${") {
		return s // fast path: most strings have no references at all
	}

	var b strings.Builder
	for rest := s; ; {
		i := strings.Index(rest, "${")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])

		ref := rest[i+2:]
		end := strings.IndexByte(ref, '}')
		if end < 0 {
			// Unterminated — not a reference. Emit it as written and let it
			// fail downstream as the malformed value it is.
			b.WriteString(rest[i:])
			break
		}
		b.WriteString(resolveRef(ref[:end], location, missing))
		rest = ref[end+1:]
	}
	return b.String()
}

// resolveRef resolves the text between ${ and }, handling the bash default
// forms VAR, VAR-default and VAR:-default.
func resolveRef(ref, location string, missing *[]missingVar) string {
	name, def := ref, ""
	hasDefault, emptyCounts := false, false
	if i := strings.IndexAny(ref, ":-"); i >= 0 {
		if ref[i] == ':' {
			// Only ':-' is a default; anything else stays part of the name
			// and will simply not resolve.
			if i+1 < len(ref) && ref[i+1] == '-' {
				name, def, hasDefault, emptyCounts = ref[:i], ref[i+2:], true, true
			}
		} else {
			name, def, hasDefault = ref[:i], ref[i+1:], true
		}
	}

	val, set := os.LookupEnv(name)
	switch {
	case set && !(val == "" && emptyCounts):
		return val // includes deliberately-empty values under plain ${VAR}
	case hasDefault:
		return def
	default:
		*missing = append(*missing, missingVar{Name: name, Location: location})
		return ""
	}
}

// expandEnvVars walks v and expands ${VAR} references in every string it
// reaches — struct fields, slice elements, and map values, at any depth.
// Fields tagged `env:"-"` are skipped along with everything beneath them.
//
// location is the dotted path used to report missing variables; it grows as
// the walk descends so the error names the exact field.
func expandEnvVars(v reflect.Value, location string, missing *[]missingVar) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(expandString(v.String(), location, missing))
		}

	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() || f.Tag.Get("env") == "-" {
				continue
			}
			expandEnvVars(v.Field(i), joinPath(location, fieldName(f)), missing)
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			expandEnvVars(v.Index(i), fmt.Sprintf("%s[%d]", location, i), missing)
		}

	case reflect.Map:
		// Map values aren't addressable, so each one is expanded into a
		// temporary and written back through SetMapIndex.
		if v.Type().Elem().Kind() != reflect.String {
			return // no nested structs in blueprint maps today
		}
		for _, k := range v.MapKeys() {
			loc := fmt.Sprintf("%s[%v]", location, k.Interface())
			expanded := expandString(v.MapIndex(k).String(), loc, missing)
			v.SetMapIndex(k, reflect.ValueOf(expanded))
		}

	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			expandEnvVars(v.Elem(), location, missing)
		}
	}
}

// fieldName returns the YAML name of a struct field, falling back to the Go
// name so error locations still make sense for untagged fields.
func fieldName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" || tag == "-" {
		return f.Name
	}
	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name
	}
	return f.Name
}

func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

// expandBlueprintEnv expands every ${VAR} reference in bp, returning a
// MissingEnvError listing all unset variables if any lacked a default.
func expandBlueprintEnv(bp *Blueprint) error {
	var missing []missingVar
	expandEnvVars(reflect.ValueOf(bp).Elem(), "", &missing)
	if len(missing) == 0 {
		return nil
	}
	sort.SliceStable(missing, func(i, j int) bool {
		return missing[i].Location < missing[j].Location
	})
	return &MissingEnvError{Missing: missing}
}
