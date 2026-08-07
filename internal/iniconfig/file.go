// Package iniconfig reads the conservative key=value subset used by the
// existing ups-pi-node configuration. It does not rewrite user files.
package iniconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type File map[string]map[string]string

func Read(path string) (File, error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer input.Close()
	result := File{}
	section := ""
	scanner := bufio.NewScanner(input)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "" {
				return nil, fmt.Errorf("parse config %s:%d: empty section", path, lineNumber)
			}
			if result[section] == nil {
				result[section] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || section == "" || key == "" {
			return nil, fmt.Errorf("parse config %s:%d", path, lineNumber)
		}
		result[section][key] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return result, nil
}

func (file File) Get(section string, key string, fallback string) string {
	if values := file[strings.ToLower(section)]; values != nil {
		if value, ok := values[strings.ToLower(key)]; ok {
			return value
		}
	}
	return fallback
}

func (file File) Set(section string, key string, value any) {
	section = strings.ToLower(strings.TrimSpace(section))
	key = strings.ToLower(strings.TrimSpace(key))
	if section == "" || key == "" {
		return
	}
	if file[section] == nil {
		file[section] = map[string]string{}
	}
	file[section][key] = fmt.Sprint(value)
}

func Write(path string, file File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write symlink config %s", path)
	}
	output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o660)
	if err != nil {
		return fmt.Errorf("open config %s for writing: %w", path, err)
	}
	defer output.Close()

	writer := bufio.NewWriter(output)
	for index, section := range sortedSections(file) {
		if index > 0 {
			if _, err := writer.WriteString("\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(writer, "[%s]\n", section); err != nil {
			return err
		}
		keys := make([]string, 0, len(file[section]))
		for key := range file[section] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := fmt.Fprintf(writer, "%s = %s\n", key, file[section][key]); err != nil {
				return err
			}
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func sortedSections(file File) []string {
	preferred := []string{"integrations", "system", "auth", "wifi", "ups", "gpio", "load", "ui", "display"}
	seen := map[string]struct{}{}
	sections := make([]string, 0, len(file))
	for _, section := range preferred {
		if _, ok := file[section]; ok {
			sections = append(sections, section)
			seen[section] = struct{}{}
		}
	}
	var extra []string
	for section := range file {
		if _, ok := seen[section]; !ok {
			extra = append(extra, section)
		}
	}
	sort.Strings(extra)
	return append(sections, extra...)
}
