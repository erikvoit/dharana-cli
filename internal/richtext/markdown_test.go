package richtext

import (
	"strings"
	"testing"
)

func TestRenderMarkdownUsesAsanaSafeXMLSubset(t *testing.T) {
	value, err := RenderMarkdown("# Acceptance criteria\n\n- **Retry** safely\n- Use `POST`\n\n[Design](https://example.com/design)\n\n```go\nreturn nil\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<body>", "<h1>Acceptance criteria</h1>", "<ul>", "<strong>Retry</strong>", "<code>POST</code>", `<a href="https://example.com/design">Design</a>`, "<pre>return nil\n</pre>", "</body>"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("expected %q in %s", expected, value)
		}
	}
	plain, err := PlainTextFromHTML(value)
	if err != nil || !strings.Contains(plain, "Acceptance criteria") || !strings.Contains(plain, "Retry safely") {
		t.Fatalf("unexpected plain text %q err=%v", plain, err)
	}
}

func TestRenderMarkdownRejectsUnsafeOrUnsupportedContent(t *testing.T) {
	for _, value := range []string{"<script>alert(1)</script>", "![image](https://example.com/x.png)", "[bad](javascript:alert(1))", "### Unsupported heading", "- item\n  > nested quote"} {
		if _, err := RenderMarkdown(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestNormalizeHTMLDropsAsanaGeneratedLinkMetadata(t *testing.T) {
	a, err := NormalizeHTML(`<body><a href="https://example.com" data-asana-accessible="true">Example</a></body>`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeHTML(`<body><a href="https://example.com">Example</a></body>`)
	if err != nil || a != b {
		t.Fatalf("expected normalized HTML equality: %q %q err=%v", a, b, err)
	}
}

func TestMarkdownRoundTripForSupportedSubset(t *testing.T) {
	source := "# Heading\n\n- **One**\n- `Two`\n\n> First line\n> second line\n\n[Design](https://example.com)"
	htmlValue, err := RenderMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	markdown, lossless, err := MarkdownFromHTML(htmlValue)
	if err != nil || !lossless {
		t.Fatalf("unexpected conversion: %q lossless=%v err=%v", markdown, lossless, err)
	}
	reRendered, err := RenderMarkdown(markdown)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NormalizeHTML(htmlValue)
	b, _ := NormalizeHTML(reRendered)
	if a != b {
		t.Fatalf("round-trip changed semantic HTML:\n%s\n%s\nmarkdown=%s", a, b, markdown)
	}
}
