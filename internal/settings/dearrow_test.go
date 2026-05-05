package settings

import "testing"

func TestNormalizeDearrowMode(t *testing.T) {
	cases := map[string]string{
		"off":       "off",
		"default":   "default",
		"casual":    "casual",
		"":          "off", // empty coerces to off
		"banana":    "off", // unknown coerces to off
		"DEFAULT":   "off", // case-sensitive per spec
		"casualish": "off", // not a prefix match
	}
	for input, want := range cases {
		if got := NormalizeDearrowMode(input); got != want {
			t.Errorf("NormalizeDearrowMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDearrowModeValuesContainsAllValid(t *testing.T) {
	wantSet := map[string]bool{"off": true, "default": true, "casual": true}
	for _, v := range DearrowModeValues {
		if !wantSet[v] {
			t.Errorf("unexpected DearrowModeValues entry: %q", v)
		}
		delete(wantSet, v)
	}
	if len(wantSet) > 0 {
		t.Errorf("missing from DearrowModeValues: %v", wantSet)
	}
}

func TestDefaultsHasDearrowModeOff(t *testing.T) {
	v, ok := Defaults["dearrow_mode"]
	if !ok {
		t.Fatal("Defaults missing dearrow_mode")
	}
	if s, _ := v.(string); s != "off" {
		t.Errorf("Defaults[dearrow_mode] = %v, want \"off\"", v)
	}
}

func TestNormalizeTranslateBackend(t *testing.T) {
	cases := map[string]string{
		"none":          TranslateBackendNone,
		"kagi_cli":      TranslateBackendKagiCLI,
		"google":        TranslateBackendGoogle,
		"deepl":         TranslateBackendDeepL,
		"openai_compat": TranslateBackendOpenAICompat,
		"api":           TranslateBackendNone,
		"local":         TranslateBackendNone,
		"":              TranslateBackendNone,
	}
	for input, want := range cases {
		if got := NormalizeTranslateBackend(input); got != want {
			t.Errorf("NormalizeTranslateBackend(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeTranslateAutoMode(t *testing.T) {
	cases := map[string]string{
		"off":        TranslateAutoOff,
		"lazy":       TranslateAutoLazy,
		"background": TranslateAutoBackground,
		"":           TranslateAutoLazy,
		"banana":     TranslateAutoLazy,
	}
	for input, want := range cases {
		if got := NormalizeTranslateAutoMode(input); got != want {
			t.Errorf("NormalizeTranslateAutoMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultsUseDisabledTranslateBackend(t *testing.T) {
	if got := Defaults["translate_backend"]; got != TranslateBackendNone {
		t.Errorf("Defaults[translate_backend] = %v, want %q", got, TranslateBackendNone)
	}
	if got := Defaults["translate_auto_mode"]; got != TranslateAutoLazy {
		t.Errorf("Defaults[translate_auto_mode] = %v, want %q", got, TranslateAutoLazy)
	}
	if got := Defaults["translate_auto_lookahead"]; got != 20 {
		t.Errorf("Defaults[translate_auto_lookahead] = %v, want 20", got)
	}
	if got := Defaults["translate_model"]; got != "" {
		t.Errorf("Defaults[translate_model] = %v, want empty string", got)
	}
}
