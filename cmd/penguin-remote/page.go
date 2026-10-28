package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// The embedded JSON table is shared with the renderer for later language changes.
// Render both initial pages once so no default-language text reaches first paint.
var localizedAppPages = buildLocalizedAppPages(appPageHTML)

func buildLocalizedAppPages(source string) map[string]string {
	_, tail, found := strings.Cut(source, `<script id="translations" type="application/json">`)
	if !found {
		panic("missing embedded UI translations")
	}
	data, _, _ := strings.Cut(tail, "</script>")
	var translations map[string]map[string]string
	if err := json.Unmarshal([]byte(data), &translations); err != nil {
		panic(fmt.Errorf("decode embedded UI translations: %w", err))
	}
	pages := make(map[string]string)
	for _, language := range []string{"zh", "en"} {
		document, err := html.Parse(strings.NewReader(source))
		if err != nil {
			panic(fmt.Errorf("parse embedded UI: %w", err))
		}
		localizeHTMLNode(document, translations[language], language)
		var page strings.Builder
		if err := html.Render(&page, document); err != nil {
			panic(fmt.Errorf("render embedded UI: %w", err))
		}
		pages[language] = page.String()
	}
	return pages
}

func localizeHTMLNode(node *html.Node, translations map[string]string, language string) {
	if node.Type == html.ElementNode {
		if node.Data == "html" {
			lang := "zh-CN"
			if language == "en" {
				lang = "en"
			}
			setHTMLAttribute(node, "lang", lang)
		}
		for _, attr := range node.Attr {
			var target string
			switch attr.Key {
			case "data-i18n":
				target = "textContent"
			case "data-i18n-aria":
				target = "aria-label"
			case "data-i18n-title":
				target = "title"
			case "data-i18n-alt":
				target = "alt"
			case "data-i18n-placeholder":
				target = "placeholder"
			default:
				continue
			}
			text, ok := translations[attr.Val]
			if !ok {
				panic("missing embedded UI translation: " + language + "/" + attr.Val)
			}
			if target == "textContent" {
				for node.FirstChild != nil {
					node.RemoveChild(node.FirstChild)
				}
				node.AppendChild(&html.Node{Type: html.TextNode, Data: text})
			} else {
				setHTMLAttribute(node, target, text)
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		localizeHTMLNode(child, translations, language)
	}
}

func setHTMLAttribute(node *html.Node, key, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
}
