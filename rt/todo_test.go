package rt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTodoError_NamesThePlaceAndTheReason(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(wd, "app", "main.nomi")
	for _, tc := range []struct {
		file, reason, want string
	}{
		{inside, "parse the header", "todo reached at app/main.nomi:12: parse the header"},
		{inside, "", "todo reached at app/main.nomi:12"},
		{"/elsewhere/main.nomi", "x", "todo reached at /elsewhere/main.nomi:12: x"},
	} {
		if got := TodoError(tc.file, 12, tc.reason).Error(); got != tc.want {
			t.Errorf("TodoError(%q, 12, %q) = %q, want %q", tc.file, tc.reason, got, tc.want)
		}
	}
}
