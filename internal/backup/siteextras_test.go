package backup

import "testing"

func TestInspectCleansBrandDetailsAndTracking(t *testing.T) {
	s := testService(t)
	c := goodContent()
	c.Settings.Brand = map[string]any{"logo": "javascript:alert(1)", "icon": "/media/i.png", "evil": 1}
	c.Settings.Details = map[string]any{"phone": "no digits", "email": `a"@b.co`, "address": "1 Road", "social": map[string]any{"facebook": "https://evil.example", "x": "https://x.com/a"}, "evil": 1}
	c.Settings.Tracking = map[string]any{"ga4": `"><script>alert(1)</script>`, "plausible": "example.com", "script": "alert(2)"}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	st := a.Content.Settings
	if len(st.Brand) != 1 || st.Brand["icon"] != "/media/i.png" {
		t.Errorf("brand: %v", st.Brand)
	}
	soc, _ := st.Details["social"].(map[string]any)
	if st.Details["phone"] != nil || st.Details["email"] != nil || st.Details["address"] != "1 Road" || st.Details["evil"] != nil || len(soc) != 1 || soc["x"] == nil {
		t.Errorf("details: %v", st.Details)
	}
	if st.Tracking["ga4"] != nil || st.Tracking["plausible"] != "example.com" || st.Tracking["script"] != nil || len(st.Tracking) != 1 {
		t.Errorf("tracking: %v", st.Tracking)
	}
}
