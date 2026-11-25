package sessionstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openfloorcontrol/ofc/floor"
)

// schemaVersionFile, in a sessions directory, holds the number of
// jsonlMigrations applied to the directory's files.
const schemaVersionFile = "schema_version"

// jsonlMigrations upgrade a sessions directory, in order. Migration i
// takes the directory from schema version i to i+1.
var jsonlMigrations = []func(dir string) error{
	migrateMetaFloor,
}

// migrateJSONL applies the migrations dir has not had yet, recording
// each in its schema_version file. A directory without that file is at
// version 0.
func migrateJSONL(dir string) error {
	path := filepath.Join(dir, schemaVersionFile)
	version := 0
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		version, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	if version > len(jsonlMigrations) {
		return fmt.Errorf("%s: schema version %d is newer than this ofc knows (%d)", path, version, len(jsonlMigrations))
	}
	for i := version; i < len(jsonlMigrations); i++ {
		if err := jsonlMigrations[i](dir); err != nil {
			return fmt.Errorf("migrate %s to schema version %d: %w", dir, i+1, err)
		}
		if err := os.WriteFile(path, []byte(strconv.Itoa(i+1)+"\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// migrateMetaFloor gives every session whose meta has a blueprint name
// but no floor the floor named like its blueprint, by appending a meta
// record.
func migrateMetaFloor(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(name, ".jsonl")
		path := filepath.Join(dir, name)
		mem := floor.NewMemoryStore()
		if err := replay(path, sid, mem); err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		meta, err := mem.GetMeta(sid)
		if errors.Is(err, floor.ErrNoSessionMeta) {
			continue
		}
		if err != nil {
			return err
		}
		if meta.Floor != "" || meta.BlueprintName == "" {
			continue
		}
		meta.Floor = meta.BlueprintName
		line, err := json.Marshal(metaRecord{Kind: "meta", SessionID: sid, Meta: meta})
		if err != nil {
			return err
		}
		if err := appendLine(path, line); err != nil {
			return err
		}
	}
	return nil
}

// appendLine appends line and a newline to the file at path and syncs it.
func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	return errors.Join(werr, serr, cerr)
}
