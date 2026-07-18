package richtext

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type Description struct {
	Format  string `json:"format" yaml:"format"`
	Content string `json:"content" yaml:"content"`
}

func (d *Description) Validate() error {
	if d == nil {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(d.Format)) != "markdown" {
		return errors.New("description format must be markdown")
	}
	_, err := RenderMarkdown(d.Content)
	return err
}

func RenderMarkdown(source string) (string, error) {
	input := []byte(strings.ReplaceAll(source, "\r\n", "\n"))
	document := goldmark.DefaultParser().Parse(text.NewReader(input))
	var out strings.Builder
	listDepth := 0
	quoteDepth := 0
	out.WriteString("<body>")
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch value := node.(type) {
		case *ast.Document:
		case *ast.Paragraph:
			if !entering {
				out.WriteByte('\n')
			}
		case *ast.TextBlock:
		case *ast.Heading:
			if entering && (value.Level > 2 || listDepth > 0) {
				return ast.WalkStop, errors.New("Markdown descriptions support only level-one and level-two headings outside lists")
			}
			tag := "h2"
			if value.Level == 1 {
				tag = "h1"
			}
			writeTag(&out, tag, entering)
			if !entering {
				out.WriteByte('\n')
			}
		case *ast.ThematicBreak:
			if entering {
				out.WriteString("<hr/>\n")
			}
		case *ast.Blockquote:
			if entering && listDepth > 0 {
				return ast.WalkStop, errors.New("blockquotes cannot be nested inside lists in Asana descriptions")
			}
			if entering {
				quoteDepth++
			} else {
				quoteDepth--
			}
			writeTag(&out, "blockquote", entering)
			if !entering {
				out.WriteByte('\n')
			}
		case *ast.List:
			if entering && quoteDepth > 0 {
				return ast.WalkStop, errors.New("lists cannot be nested inside blockquotes in Asana descriptions")
			}
			if entering {
				listDepth++
			} else {
				listDepth--
			}
			tag := "ul"
			if value.IsOrdered() {
				tag = "ol"
			}
			writeTag(&out, tag, entering)
			if !entering {
				out.WriteByte('\n')
			}
		case *ast.ListItem:
			writeTag(&out, "li", entering)
		case *ast.CodeBlock:
			if entering {
				if listDepth > 0 {
					return ast.WalkStop, errors.New("code blocks cannot be nested inside lists in Asana descriptions")
				}
				out.WriteString("<pre>")
				out.WriteString(html.EscapeString(string(value.Lines().Value(input))))
				out.WriteString("</pre>\n")
				return ast.WalkSkipChildren, nil
			}
		case *ast.FencedCodeBlock:
			if entering {
				if listDepth > 0 {
					return ast.WalkStop, errors.New("code blocks cannot be nested inside lists in Asana descriptions")
				}
				out.WriteString("<pre>")
				out.WriteString(html.EscapeString(string(value.Lines().Value(input))))
				out.WriteString("</pre>\n")
				return ast.WalkSkipChildren, nil
			}
		case *ast.CodeSpan:
			writeTag(&out, "code", entering)
		case *ast.Emphasis:
			tag := "em"
			if value.Level == 2 {
				tag = "strong"
			}
			writeTag(&out, tag, entering)
		case *ast.Link:
			if entering {
				destination, err := safeLink(string(value.Destination))
				if err != nil {
					return ast.WalkStop, err
				}
				out.WriteString(`<a href="` + html.EscapeString(destination) + `">`)
			} else {
				out.WriteString("</a>")
			}
		case *ast.AutoLink:
			if entering {
				destination, err := safeLink(string(value.URL(input)))
				if err != nil {
					return ast.WalkStop, err
				}
				out.WriteString(`<a href="` + html.EscapeString(destination) + `">` + html.EscapeString(destination) + `</a>`)
			}
		case *ast.Text:
			if entering {
				out.WriteString(html.EscapeString(string(value.Segment.Value(input))))
				if value.HardLineBreak() {
					out.WriteString("<br/>\n")
				} else if value.SoftLineBreak() {
					out.WriteByte('\n')
				}
			}
		case *ast.String:
			if entering {
				out.WriteString(html.EscapeString(string(value.Value)))
			}
		case *ast.RawHTML, *ast.HTMLBlock:
			if entering {
				return ast.WalkStop, errors.New("raw HTML is not supported in Markdown descriptions")
			}
		case *ast.Image:
			if entering {
				return ast.WalkStop, errors.New("Markdown images are not supported in task descriptions")
			}
		default:
			if entering {
				return ast.WalkStop, fmt.Errorf("unsupported Markdown node %s", node.Kind().String())
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(out.String()) + "</body>"
	if err := validateXML(value); err != nil {
		return "", fmt.Errorf("render Markdown description: %w", err)
	}
	return value, nil
}

func PlainTextFromHTML(value string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var out strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch item := token.(type) {
		case xml.CharData:
			out.Write([]byte(item))
		case xml.EndElement:
			switch item.Name.Local {
			case "h1", "h2", "li", "blockquote", "pre", "ul", "ol":
				out.WriteByte('\n')
			}
		case xml.StartElement:
			if item.Name.Local == "hr" {
				out.WriteString("---\n")
			}
		}
	}
	return normalizeLines(out.String()), nil
}

func MarkdownFromHTML(value string) (string, bool, error) {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var out strings.Builder
	var links []string
	quoteDepth := 0
	var lists []struct {
		ordered bool
		next    int
	}
	lossless := true
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		switch item := token.(type) {
		case xml.CharData:
			value := string(item)
			if quoteDepth > 0 && strings.Contains(value, "\n") {
				parts := strings.Split(value, "\n")
				for i, part := range parts {
					if i > 0 {
						out.WriteByte('\n')
						if part != "" || i < len(parts)-1 {
							out.WriteString("> ")
						}
					}
					out.WriteString(part)
				}
			} else {
				out.WriteString(value)
			}
		case xml.StartElement:
			switch item.Name.Local {
			case "body":
			case "h1":
				out.WriteString("# ")
			case "h2":
				out.WriteString("## ")
			case "strong":
				out.WriteString("**")
			case "em":
				out.WriteString("*")
			case "s":
				lossless = false
				out.WriteString("~~")
			case "u":
				lossless = false
			case "code":
				out.WriteString("`")
			case "pre":
				out.WriteString("```\n")
			case "blockquote":
				out.WriteString("> ")
				quoteDepth++
			case "ul", "ol":
				lists = append(lists, struct {
					ordered bool
					next    int
				}{ordered: item.Name.Local == "ol", next: 1})
			case "li":
				if len(lists) > 0 {
					list := &lists[len(lists)-1]
					out.WriteString(strings.Repeat("  ", len(lists)-1))
					if list.ordered {
						out.WriteString(fmt.Sprintf("%d. ", list.next))
						list.next++
					} else {
						out.WriteString("- ")
					}
				}
			case "a":
				href := ""
				for _, attr := range item.Attr {
					if attr.Name.Local == "href" {
						href = attr.Value
					}
				}
				links = append(links, href)
				if href == "" {
					lossless = false
				}
				out.WriteString("[")
			case "hr":
				out.WriteString("\n---\n")
			default:
				lossless = false
			}
		case xml.EndElement:
			switch item.Name.Local {
			case "h1", "h2":
				out.WriteString("\n\n")
			case "strong":
				out.WriteString("**")
			case "em":
				out.WriteString("*")
			case "s":
				out.WriteString("~~")
			case "code":
				out.WriteString("`")
			case "pre":
				out.WriteString("\n```\n\n")
			case "blockquote":
				quoteDepth--
				out.WriteString("\n\n")
			case "li":
				out.WriteByte('\n')
			case "ul", "ol":
				if len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
				if len(lists) == 0 {
					out.WriteByte('\n')
				}
			case "a":
				href := ""
				if len(links) > 0 {
					href = links[len(links)-1]
					links = links[:len(links)-1]
				}
				if href == "" {
					out.WriteString("]")
				} else {
					out.WriteString("](" + href + ")")
				}
			}
		}
	}
	return normalizeMarkdown(out.String()), lossless, nil
}

func NormalizeHTML(value string) (string, error) {
	var out bytes.Buffer
	decoder := xml.NewDecoder(strings.NewReader(value))
	encoder := xml.NewEncoder(&out)
	preDepth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local == "pre" {
				preDepth++
			}
			if start.Name.Local == "a" {
				attrs := start.Attr[:0]
				for _, attr := range start.Attr {
					if attr.Name.Local == "href" {
						attrs = append(attrs, attr)
					}
				}
				start.Attr = attrs
			}
			token = start
		}
		if chars, ok := token.(xml.CharData); ok && preDepth == 0 && strings.TrimSpace(string(chars)) == "" && strings.ContainsAny(string(chars), "\n\r\t") {
			continue
		}
		if err := encoder.EncodeToken(token); err != nil {
			return "", err
		}
		if end, ok := token.(xml.EndElement); ok && end.Name.Local == "pre" {
			preDepth--
		}
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func writeTag(out *strings.Builder, tag string, entering bool) {
	if entering {
		out.WriteByte('<')
		out.WriteString(tag)
		out.WriteByte('>')
		return
	}
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteByte('>')
}

func safeLink(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("Markdown links must use an absolute http, https, or mailto URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return parsed.String(), nil
	default:
		return "", errors.New("Markdown links must use an absolute http, https, or mailto URL")
	}
}

func validateXML(value string) error {
	decoder := xml.NewDecoder(strings.NewReader(value))
	for {
		_, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func normalizeLines(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeMarkdown(value string) string {
	value = normalizeLines(value)
	for strings.Contains(value, "\n\n\n") {
		value = strings.ReplaceAll(value, "\n\n\n", "\n\n")
	}
	return value
}
