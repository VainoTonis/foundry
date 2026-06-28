package webui

import (
	"strings"
	"testing"
)

func TestTemplateMarkdownRendersBasicBlocks(t *testing.T) {
	out := string(templateMarkdown("# Title\n\n- one\n- `two`\n\n```go\nfmt.Println(\"hi\")\n```"))
	for _, want := range []string{
		"<h1>Title</h1>",
		"<ul><li>one</li><li><code>two</code></li></ul>",
		"<pre><code>fmt.Println(&#34;hi&#34;)\n</code></pre>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in %q", want, out)
		}
	}
}

func TestTemplateMarkdownRendersChatStyleLists(t *testing.T) {
	out := string(templateMarkdown("Common types include:\n\n    **Written content:** blog posts\n    **Video:** tutorials\n\n1. **Audience research** - know people.\n2. **Production** - make thing."))
	for _, want := range []string{
		"<p>Common types include:</p>",
		"<ul><li><strong>Written content:</strong> blog posts</li><li><strong>Video:</strong> tutorials</li></ul>",
		"<ol><li><strong>Audience research</strong> - know people.</li><li><strong>Production</strong> - make thing.</li></ol>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in %q", want, out)
		}
	}
}

func TestTemplateMarkdownEscapesHTML(t *testing.T) {
	out := string(templateMarkdown("<script>alert(1)</script>"))
	if strings.Contains(out, "<script>") {
		t.Fatalf("expected script tag escaped, got %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("expected escaped html, got %q", out)
	}
}
