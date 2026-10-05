package git

import (
	"testing"
)

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		preserveCase bool
		expected     string
	}{
		{
			name:     "simple lowercase string",
			input:    "feature",
			expected: "feature",
		},
		{
			name:     "string with spaces",
			input:    "new feature branch",
			expected: "new-feature-branch",
		},
		{
			name:     "mixed case string",
			input:    "FeAtUrE BrAnCh",
			expected: "feature-branch",
		},
		{
			name:     "string with special characters",
			input:    "feature!@#$%^&*()",
			expected: "feature",
		},
		{
			name:     "string with allowed special characters",
			input:    "feature/sub_branch.v1",
			expected: "feature/sub_branch.v1",
		},
		{
			name:     "string with multiple dashes",
			input:    "feature---branch",
			expected: "feature-branch",
		},
		{
			name:     "string with leading and trailing dashes",
			input:    "-feature-branch-",
			expected: "feature-branch",
		},
		{
			name:     "string with leading and trailing slashes",
			input:    "/feature/branch/",
			expected: "feature/branch",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "complex mixed case with special chars",
			input:    "USER/Feature Branch!@#$%^&*()/v1.0",
			expected: "user/feature-branch/v1.0",
		},
		{
			name:         "preserving case keeps an upper-case prefix",
			input:        "TASK-5636-Vis-6",
			preserveCase: true,
			expected:     "TASK-5636-Vis-6",
		},
		{
			name:         "preserving case still strips disallowed characters",
			input:        "TASK-1234 Fix The Thing!@#",
			preserveCase: true,
			expected:     "TASK-1234-Fix-The-Thing",
		},
		{
			name:         "preserving case still collapses and trims dashes",
			input:        "-TASK---Foo-",
			preserveCase: true,
			expected:     "TASK-Foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranchName(tt.input, tt.preserveCase)
			if got != tt.expected {
				t.Errorf("sanitizeBranchName(%q, %v) = %q, want %q", tt.input, tt.preserveCase, got, tt.expected)
			}
		})
	}
}
