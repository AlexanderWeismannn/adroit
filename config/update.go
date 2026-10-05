package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// UpdateConfigFile changes named top-level keys of config.json and leaves every
// other key exactly as it was. A nil value deletes its key.
//
// It works on the raw JSON rather than on Config, because marshalling the struct
// would silently drop anything the struct does not know about — a key from a
// different version of cs, or one a user added by hand. Nothing wrote an existing
// config file before this, so that loss had never been possible; a command whose
// whole job is to edit one setting must not introduce it.
//
// Ordering is not preserved: Go marshals a map with its keys sorted, so a file
// edited here comes back alphabetical. Every value survives, which is the part
// that matters.
func UpdateConfigFile(changes map[string]any) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	configPath := filepath.Join(configDir, ConfigFileName)

	raw := map[string]json.RawMessage{}
	existing, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &raw); err != nil {
				// Refuse rather than overwrite. The file is the user's, and
				// replacing one we cannot parse would destroy whatever is in it —
				// including the very thing that needs hand-fixing.
				return fmt.Errorf("config file at %s is not valid JSON, so it will not be rewritten: %w", configPath, err)
			}
		}
	case os.IsNotExist(err):
		// Start from the defaults so a file created here looks like one created
		// by a first run, rather than holding a lone "theme" key.
		defaults, marshalErr := json.Marshal(DefaultConfig())
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal default config: %w", marshalErr)
		}
		if err := json.Unmarshal(defaults, &raw); err != nil {
			return fmt.Errorf("failed to read default config: %w", err)
		}
	default:
		return fmt.Errorf("failed to read config file: %w", err)
	}

	for key, value := range changes {
		if value == nil {
			delete(raw, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to encode %q: %w", key, err)
		}
		raw[key] = encoded
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	data = append(data, '\n')

	return writeFileAtomic(configPath, data, 0644)
}

// writeFileAtomic writes via a temporary file in the same directory and renames
// over the target, so an interrupted write cannot leave a truncated file where
// a working one used to be. The data is synced before the rename, or a crash can
// still leave the new name pointing at blocks that were never written. Same
// directory because a rename across filesystems is not atomic and may not be
// permitted at all.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Harmless once the rename has happened; the file no longer exists.
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temporary file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set file permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// ReadConfigKey returns one top-level key's raw JSON, and whether it was present.
// Used to read a setting back without going through Config, which would report a
// zero value for a key that is simply absent.
func ReadConfigKey(key string) (json.RawMessage, bool, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(filepath.Join(configDir, ConfigFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	value, ok := raw[key]
	return value, ok, nil
}
