package self

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := Release{
			TagName: "v1.2.0",
			Name:    "Release v1.2.0",
			HTMLURL: "https://github.com/JLCode-tech/gkebnkctl/releases/tag/v1.2.0",
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	ctx := context.Background()
	// test parsing with direct request
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.0" {
		t.Errorf("expected tag v1.2.0, got %s", rel.TagName)
	}
}
