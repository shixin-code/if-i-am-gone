package templates

import "testing"

func TestRenderSupportsBraceAndAnglePlaceholders(t *testing.T) {
	got := Render("第<N>天，您好 {name}", map[string]string{
		"N":    "3",
		"name": "张三",
	})
	if got != "第3天，您好 张三" {
		t.Fatalf("got %q", got)
	}
}
