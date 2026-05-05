package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLoginPageRender(t *testing.T) {
	var buf bytes.Buffer
	err := LoginPage(newTestPageProps(), "csrf-abc", "", "/feed").Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	checks := []string{
		`value="csrf-abc"`,
		`name="_csrf_token"`,
		`action="/login"`,
		`name="next" value="/feed"`,
		`id="username"`,
		`id="password"`,
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing: %s", check)
		}
	}

	if strings.Contains(html, `class="error"`) {
		t.Error("error div should not appear when errorMsg is empty")
	}
}

func TestLoginPageWithError(t *testing.T) {
	var buf bytes.Buffer
	err := LoginPage(newTestPageProps(), "tok", "Invalid credentials", "").Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, "Invalid credentials") {
		t.Error("error message not rendered")
	}
	if !strings.Contains(html, `class="error"`) {
		t.Error("error div not present")
	}
	if !strings.Contains(html, `name="next" value="/"`) {
		t.Error("default next value should be /")
	}
}
