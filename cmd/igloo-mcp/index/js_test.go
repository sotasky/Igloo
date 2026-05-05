package index

import "testing"

func TestScanJSSymbols(t *testing.T) {
	src := `
function loadFeed(cursor) {
  return apiJson('/api/feed/rsshub');
}

class FeedManager {
  constructor(el) {
    this.el = el;
  }
  refresh() {
    return this.loadData();
  }
}

const handleLike = (id) => {
  apiFetch('/api/feed/like/' + id, { method: 'POST' });
};

let processItem = (item) => item.id;

async function fetchData() {}
`
	syms := ScanJSSymbols(src, "static/js/feed.js")

	counts := map[string]int{}
	for _, s := range syms {
		counts[s.Kind]++
		if s.Language != "js" {
			t.Errorf("expected language js, got %s", s.Language)
		}
		if s.File != "static/js/feed.js" {
			t.Errorf("expected file static/js/feed.js, got %s", s.File)
		}
	}
	// loadFeed, fetchData = 2 functions
	// FeedManager = 1 class
	// handleLike, processItem = 2 arrow functions
	if counts["function"] != 4 {
		t.Errorf("expected 4 functions (named + arrow), got %d", counts["function"])
	}
	if counts["class"] != 1 {
		t.Errorf("expected 1 class, got %d", counts["class"])
	}
}

func TestScanJSExportedSymbols(t *testing.T) {
	src := `export function initDates(container) {}
export async function fetchData() {}
export const handleLike = (id) => {};
export default function init() {}
export class FeedManager {}
`
	syms := ScanJSSymbols(src, "static/js/src/feed/dates.js")
	if len(syms) < 5 {
		t.Fatalf("expected 5 exported symbols, got %d", len(syms))
	}
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	for _, want := range []string{"initDates", "fetchData", "handleLike", "init", "FeedManager"} {
		if !names[want] {
			t.Errorf("missing exported symbol: %s", want)
		}
	}
}

func TestScanJS(t *testing.T) {
	src := `
function loadFeed() {
  apiJson('/api/feed/rsshub', { method: 'GET' });
  apiFetch('/api/feed/like/' + id, { method: 'POST' });
  fetch('/api/video/status');
}
`
	calls := ScanJS(src, "static/js/feed.js")
	if len(calls) < 3 {
		t.Fatalf("expected at least 3 api calls, got %d", len(calls))
	}
	if calls[0].URL != "/api/feed/rsshub" {
		t.Errorf("unexpected url: %s", calls[0].URL)
	}
	if calls[1].Method != "POST" {
		t.Errorf("expected POST, got %s", calls[1].Method)
	}
}
