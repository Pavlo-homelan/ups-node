// Package iniconfig reads the conservative key=value subset used by the
// existing ups-pi-node configuration. It does not rewrite user files.
package iniconfig

import (
	"bufio"
	"fmt"
	"os"
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
