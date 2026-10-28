//go:build treesitter

package structure

import (
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	goGrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
	pythonGrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rustGrammar "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	typescriptGrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type symbol struct {
	name, text, body string
	start, end       int
}

func languageFor(path string) *sitter.Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return sitter.NewLanguage(goGrammar.Language())
	case ".ts":
		return sitter.NewLanguage(typescriptGrammar.LanguageTypescript())
	case ".tsx":
		return sitter.NewLanguage(typescriptGrammar.LanguageTSX())
	case ".py":
		return sitter.NewLanguage(pythonGrammar.Language())
	case ".rs":
		return sitter.NewLanguage(rustGrammar.Language())
	default:
		return nil
	}
}

func symbols(lang *sitter.Language, source string) map[string]symbol {
	data := []byte(source)
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil
	}
	tree := parser.Parse(data, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()
	result := map[string]symbol{}
	var walk func(*sitter.Node, string)
	walk = func(node *sitter.Node, scope string) {
		if node == nil {
			return
		}
		kind := node.Kind()
		if kind == "class_definition" || kind == "class_declaration" || kind == "impl_item" {
			name := node.ChildByFieldName("name")
			if kind == "impl_item" {
				name = node.ChildByFieldName("type")
			}
			if name != nil {
				scope += name.Utf8Text(data) + "."
			}
		}
		function := kind == "function_declaration" || kind == "method_declaration" || kind == "method_definition" || kind == "function_definition" || kind == "function_item"
		if kind == "variable_declarator" {
			value := node.ChildByFieldName("value")
			function = value != nil && (value.Kind() == "arrow_function" || value.Kind() == "function_expression")
		}
		if function {
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				name := scope + nameNode.Utf8Text(data)
				if kind == "method_declaration" {
					if receiver := node.ChildByFieldName("receiver"); receiver != nil {
						name = receiver.Utf8Text(data) + "." + name
					}
				}
				body := node.ChildByFieldName("body")
				if kind == "variable_declarator" {
					body = node.ChildByFieldName("value")
				}
				bodyText := ""
				if body != nil {
					bodyText = body.Utf8Text(data)
				}
				result[name] = symbol{name: name, text: node.Utf8Text(data), body: strings.TrimSpace(bodyText), start: int(node.StartPosition().Row) + 1, end: int(node.EndPosition().Row) + 1}
				scope = name + "."
			}
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i), scope)
		}
	}
	walk(tree.RootNode(), "")
	return result
}

// Classify compares function syntax trees for supported languages. The default
// build omits this parser and its cgo dependencies.
func Classify(path, before, after string) []Change {
	lang := languageFor(path)
	if lang == nil {
		return nil
	}
	old := symbols(lang, before)
	newSymbols := symbols(lang, after)
	var changes []Change
	removed := map[string]symbol{}
	added := map[string]symbol{}
	for name, prior := range old {
		if current, ok := newSymbols[name]; ok {
			if prior.text != current.text {
				changes = append(changes, Change{Kind: "function-changed", Name: name, StartLine: current.start, EndLine: current.end})
			}
		} else {
			removed[name] = prior
		}
	}
	for name, current := range newSymbols {
		if _, ok := old[name]; !ok {
			added[name] = current
		}
	}
	for oldName, prior := range removed {
		if prior.body == "" {
			continue
		}
		for newName, current := range added {
			if current.body == prior.body {
				changes = append(changes, Change{Kind: "function-renamed", Name: oldName + " → " + newName, StartLine: current.start, EndLine: current.end})
				delete(removed, oldName)
				delete(added, newName)
				break
			}
		}
	}
	for name, current := range added {
		changes = append(changes, Change{Kind: "function-added", Name: name, StartLine: current.start, EndLine: current.end})
	}
	for name, prior := range removed {
		changes = append(changes, Change{Kind: "function-removed", Name: name, StartLine: prior.start, EndLine: prior.end})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].StartLine == changes[j].StartLine {
			return changes[i].Name < changes[j].Name
		}
		return changes[i].StartLine < changes[j].StartLine
	})
	return changes
}
