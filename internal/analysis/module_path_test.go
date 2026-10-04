package analysis

import (
	"path/filepath"
	"testing"
)

func TestResolveModulePath(t *testing.T) {
	tests := []struct {
		name        string
		projectRoot string
		modulePath  []string
		want        string
	}{
		{
			name:        "simple module",
			projectRoot: "/project",
			modulePath:  []string{"math"},
			want:        "/project/math.nomi",
		},
		{
			name:        "nested module",
			projectRoot: "/project",
			modulePath:  []string{"models", "user"},
			want:        "/project/models/user.nomi",
		},
		{
			name:        "single segment",
			projectRoot: "/project/src",
			modulePath:  []string{"utils"},
			want:        "/project/src/utils.nomi",
		},
		{
			name:        "parent relative module",
			projectRoot: "/project/tests/01-foundations",
			modulePath:  []string{"..", "test_helpers"},
			want:        filepath.Clean("/project/tests/test_helpers.nomi"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveModulePath(tt.projectRoot, tt.modulePath)
			if got != tt.want {
				t.Errorf("ResolveModulePath() = %q, want %q", got, tt.want)
			}
		})
	}
}
