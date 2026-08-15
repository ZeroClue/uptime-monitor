package storage

import (
	"encoding/json"
	"strings"
)

func parseTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil
	}
	return tags
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func matchesTagQuery(hostTags []string, query string) bool {
	if query == "" {
		return true
	}
	parts := strings.Split(query, " ")
	if len(parts) == 1 {
		return contains(hostTags, parts[0])
	}
	if parts[1] == "AND" {
		return contains(hostTags, parts[0]) && contains(hostTags, parts[2])
	}
	if parts[1] == "OR" {
		return contains(hostTags, parts[0]) || contains(hostTags, parts[2])
	}
	return false
}

type resolutionConfig struct {
	table    string
	valueCol string
}

var resolutionMap = map[string]resolutionConfig{
	"raw": {"samples_raw", "value"},
	"1m":  {"samples_1m", "value_avg"},
	"1h":  {"samples_1h", "value_avg"},
}

type HostStatusInfo struct {
	ConsecutiveFails int
}