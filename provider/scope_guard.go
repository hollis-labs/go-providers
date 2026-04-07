package provider

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ScopeViolation represents a detected scope violation.
type ScopeViolation struct {
	Type        string // "file_access", "tool_use", "system_operation"
	Description string
	Event       StreamEvent
}

func (sv *ScopeViolation) Error() string {
	return fmt.Sprintf("Scope violation (%s): %s", sv.Type, sv.Description)
}

// ScopeGuard monitors stream events for operations outside the allowed scope.
// It can detect file access patterns, tool usage, and system operations that
// might be outside the current task scope.
type ScopeGuard struct {
	allowedPatterns []string
	violationMode   string // "log" or "kill"

	// Compiled regex patterns for performance
	compiledPatterns []*regexp.Regexp
}

// NewScopeGuard creates a new scope guard with the given allowed patterns.
func NewScopeGuard(allowedPatterns []string, violationMode string) *ScopeGuard {
	sg := &ScopeGuard{
		allowedPatterns: allowedPatterns,
		violationMode:   violationMode,
		compiledPatterns: make([]*regexp.Regexp, 0, len(allowedPatterns)),
	}

	// Compile patterns for better performance
	for _, pattern := range allowedPatterns {
		// Convert glob pattern to regex
		regexPattern := globToRegex(pattern)
		if compiled, err := regexp.Compile(regexPattern); err == nil {
			sg.compiledPatterns = append(sg.compiledPatterns, compiled)
		}
	}

	return sg
}

// CheckEvent examines a stream event for scope violations.
func (sg *ScopeGuard) CheckEvent(event StreamEvent) *ScopeViolation {
	switch event.Type {
	case "tool_use":
		return sg.checkToolUse(event)
	case "delta":
		return sg.checkTextContent(event)
	case "error":
		return sg.checkError(event)
	default:
		return nil
	}
}

// checkToolUse examines tool usage for scope violations.
func (sg *ScopeGuard) checkToolUse(event StreamEvent) *ScopeViolation {
	if event.ToolUse == nil {
		return nil
	}

	toolName := event.ToolUse.Name

	// Check for potentially dangerous tool operations
	dangerousTools := []string{
		"rm", "delete", "remove", "unlink",
		"sudo", "su", "chmod", "chown",
		"wget", "curl", "git", "npm", "pip",
		"docker", "systemctl", "service",
	}

	for _, dangerous := range dangerousTools {
		if strings.Contains(toolName, dangerous) {
			// Check if this operation is allowed by patterns
			if !sg.isAllowedOperation(toolName) {
				return &ScopeViolation{
					Type:        "tool_use",
					Description: fmt.Sprintf("Potentially dangerous tool usage: %s", toolName),
					Event:       event,
				}
			}
		}
	}

	// Check file access patterns in tool input
	if input, ok := event.ToolUse.Input["file_path"].(string); ok {
		if violation := sg.checkFileAccess(input); violation != nil {
			return &ScopeViolation{
				Type:        "file_access",
				Description: fmt.Sprintf("File access outside scope: %s", input),
				Event:       event,
			}
		}
	}

	return nil
}

// checkTextContent examines text deltas for potential scope violations.
func (sg *ScopeGuard) checkTextContent(event StreamEvent) *ScopeViolation {
	content := event.Content

	// Look for file paths or system commands in the text
	suspiciousPatterns := []string{
		`/etc/`, `/usr/`, `/bin/`, `/var/`,
		`sudo `, `rm -rf`, `chmod `,
		`~/.*/.env`, `~/.ssh/`,
	}

	for _, pattern := range suspiciousPatterns {
		if matched, _ := regexp.MatchString(pattern, content); matched {
			// Extract the matched part for more specific checking
			if !sg.isAllowedByPatterns(content) {
				return &ScopeViolation{
					Type:        "system_operation",
					Description: fmt.Sprintf("Suspicious system operation in text: pattern %s", pattern),
					Event:       event,
				}
			}
		}
	}

	return nil
}

// checkError examines error events for scope-related issues.
func (sg *ScopeGuard) checkError(event StreamEvent) *ScopeViolation {
	// For now, we don't consider errors as scope violations
	// But we could extend this to detect permission errors, etc.
	return nil
}

// checkFileAccess checks if a file path access is within allowed scope.
func (sg *ScopeGuard) checkFileAccess(path string) *ScopeViolation {
	// Normalize the path
	normalizedPath := filepath.Clean(path)

	// Check against allowed patterns
	if sg.isAllowedByPatterns(normalizedPath) {
		return nil
	}

	return &ScopeViolation{
		Type:        "file_access",
		Description: fmt.Sprintf("File access outside allowed scope: %s", normalizedPath),
	}
}

// isAllowedOperation checks if a tool operation is allowed.
func (sg *ScopeGuard) isAllowedOperation(operation string) bool {
	// If "*" is in allowed patterns, allow everything
	for _, pattern := range sg.allowedPatterns {
		if pattern == "*" {
			return true
		}
	}

	// Check against specific patterns
	return sg.isAllowedByPatterns(operation)
}

// isAllowedByPatterns checks if a string matches any of the allowed patterns.
func (sg *ScopeGuard) isAllowedByPatterns(input string) bool {
	for _, regex := range sg.compiledPatterns {
		if regex.MatchString(input) {
			return true
		}
	}
	return false
}

// globToRegex converts a glob pattern to a regular expression.
func globToRegex(glob string) string {
	// Escape special regex characters except * and ?
	regex := strings.ReplaceAll(glob, ".", "\\.")
	regex = strings.ReplaceAll(regex, "+", "\\+")
	regex = strings.ReplaceAll(regex, "^", "\\^")
	regex = strings.ReplaceAll(regex, "$", "\\$")
	regex = strings.ReplaceAll(regex, "(", "\\(")
	regex = strings.ReplaceAll(regex, ")", "\\)")
	regex = strings.ReplaceAll(regex, "[", "\\[")
	regex = strings.ReplaceAll(regex, "]", "\\]")
	regex = strings.ReplaceAll(regex, "{", "\\{")
	regex = strings.ReplaceAll(regex, "}", "\\}")
	regex = strings.ReplaceAll(regex, "|", "\\|")

	// Convert glob wildcards to regex
	regex = strings.ReplaceAll(regex, "*", ".*")
	regex = strings.ReplaceAll(regex, "?", ".")

	// Anchor the regex
	return "^" + regex + "$"
}