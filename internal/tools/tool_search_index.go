package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/kljensen/snowball/english"
)

type toolSearchCache struct {
	mu        sync.Mutex
	documents map[string]*toolSearchCachedDocument
	index     *toolSearchIndex
}

type toolSearchCachedDocument struct {
	definition  toolSearchDefinition
	document    toolSearchDocument
	frequencies map[string]int
}

type toolSearchDocument struct {
	name            string
	nameTokens      []string
	nameStem        string
	summaryTerms    map[string]bool
	termFrequencies map[string]int
	description     string
	parameters      json.RawMessage
	source          *toolSearchSource
	length          int
}

type toolSearchPosting struct {
	document  int
	frequency int
}

type toolSearchIndex struct {
	documents []toolSearchDocument
	full      toolSearchField
	primary   toolSearchField
}

type toolSearchField struct {
	postings map[string][]toolSearchPosting
	lengths  []int
	average  float64
}

type toolSearchDefinition struct {
	agentcore.ToolDefinition
	RetrievalTerms []string          `json:"retrieval_terms,omitempty"`
	Source         *toolSearchSource `json:"source,omitempty"`
}

type toolSearchSource struct {
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	Connection string `json:"connection"`
}

func toolSearchDefinitions(defs []agentcore.ToolDefinition) []toolSearchDefinition {
	entries := make([]toolSearchDefinition, len(defs))
	for position, def := range defs {
		entries[position] = toolSearchDefinition{ToolDefinition: def, RetrievalTerms: def.SearchTerms}
	}
	return entries
}

func (cache *toolSearchCache) snapshot(defs []agentcore.ToolDefinition) (*toolSearchIndex, error) {
	return cache.snapshotCatalog(toolSearchDefinitions(defs))
}

func (cache *toolSearchCache) snapshotCatalog(entries []toolSearchDefinition) (*toolSearchIndex, error) {
	entries = append([]toolSearchDefinition(nil), entries...)
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name < entries[right].Name })
	cache.mu.Lock()
	defer cache.mu.Unlock()
	documents := make(map[string]*toolSearchCachedDocument, len(entries))
	changed := cache.index == nil || len(entries) != len(cache.index.documents)
	for _, definition := range entries {
		cached := cache.documents[definition.Name]
		if cached != nil && cached.definition.Description == definition.Description &&
			slices.Equal(cached.definition.RetrievalTerms, definition.RetrievalTerms) &&
			toolSearchSourceEqual(cached.definition.Source, definition.Source) &&
			toolSearchJSONEqual(definition.Parameters, cached.definition.Parameters) {
			documents[definition.Name] = cached
			continue
		}
		encoded, err := json.Marshal(definition)
		if err != nil {
			return nil, fmt.Errorf("encode tool %q: %w", definition.Name, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var frozen toolSearchDefinition
		if err := decoder.Decode(&frozen); err != nil {
			return nil, fmt.Errorf("decode tool %q: %w", definition.Name, err)
		}
		parameters, err := json.Marshal(frozen.Parameters)
		if err != nil {
			return nil, fmt.Errorf("encode schema for tool %q: %w", definition.Name, err)
		}
		text := frozen.Name + " " + frozen.Description + " " + strings.Join(frozen.RetrievalTerms, " ") + " " + strings.Join(toolSearchSchemaText(frozen.Parameters), " ")
		tokens := toolSearchTokens(text)
		frequencies := make(map[string]int)
		for _, token := range tokens {
			frequencies[token]++
			if stem := toolSearchStemKey(token); stem != "" {
				frequencies[stem]++
			}
		}
		summary, _, _ := strings.Cut(frozen.Description, "\n")
		summary, _, _ = strings.Cut(summary, ". ")
		summaryTerms := make(map[string]bool)
		for _, token := range toolSearchTokens(frozen.Name + " " + summary + " " + strings.Join(frozen.RetrievalTerms, " ")) {
			summaryTerms[token] = true
			if stem := toolSearchStemKey(token); stem != "" {
				summaryTerms[stem] = true
			}
		}
		documents[definition.Name] = &toolSearchCachedDocument{
			definition: frozen,
			document: toolSearchDocument{
				name: frozen.Name, nameTokens: toolSearchTokens(frozen.Name), nameStem: toolSearchStemKey(strings.ToLower(frozen.Name)),
				summaryTerms: summaryTerms, termFrequencies: frequencies, description: frozen.Description,
				parameters: parameters, source: frozen.Source, length: len(tokens),
			},
			frequencies: frequencies,
		}
		changed = true
	}
	if !changed {
		return cache.index, nil
	}
	index := &toolSearchIndex{
		full:    toolSearchField{postings: make(map[string][]toolSearchPosting)},
		primary: toolSearchField{postings: make(map[string][]toolSearchPosting)},
	}
	for _, definition := range entries {
		cached := documents[definition.Name]
		document := len(index.documents)
		index.documents = append(index.documents, cached.document)
		for token, frequency := range cached.frequencies {
			index.full.postings[token] = append(index.full.postings[token], toolSearchPosting{document, frequency})
		}
		for token := range cached.document.summaryTerms {
			index.primary.postings[token] = append(index.primary.postings[token], toolSearchPosting{document, 1})
		}
		index.full.lengths = append(index.full.lengths, cached.document.length)
		index.primary.lengths = append(index.primary.lengths, len(cached.document.summaryTerms))
		index.full.average += float64(cached.document.length)
		index.primary.average += float64(len(cached.document.summaryTerms))
	}
	if len(index.documents) > 0 {
		index.full.average /= float64(len(index.documents))
		index.primary.average /= float64(len(index.documents))
	}
	cache.documents, cache.index = documents, index
	return index, nil
}

func toolSearchSourceEqual(left, right *toolSearchSource) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func toolSearchJSONEqual(current, frozen any) bool {
	switch value := current.(type) {
	case nil, string, bool:
		return current == frozen
	case map[string]any:
		if value == nil {
			other, ok := frozen.(map[string]any)
			return frozen == nil || (ok && other == nil)
		}
		other, ok := frozen.(map[string]any)
		if !ok || other == nil || len(value) != len(other) {
			return false
		}
		for key, child := range value {
			previous, exists := other[key]
			if !exists || !toolSearchJSONEqual(child, previous) {
				return false
			}
		}
		return true
	case []any:
		if value == nil {
			return frozen == nil
		}
		other, ok := frozen.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for position, child := range value {
			if !toolSearchJSONEqual(child, other[position]) {
				return false
			}
		}
		return true
	case []string:
		if value == nil {
			return frozen == nil
		}
		other, ok := frozen.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for position, child := range value {
			if child != other[position] {
				return false
			}
		}
		return true
	case json.Number:
		return value == frozen
	default:
		encoded, err := json.Marshal(current)
		if err != nil {
			return false
		}
		if number, ok := frozen.(json.Number); ok {
			return string(encoded) == string(number)
		}
		previous, err := json.Marshal(frozen)
		return err == nil && bytes.Equal(encoded, previous)
	}
}

func toolSearchTokens(text string) []string {
	var expanded strings.Builder
	characters := []rune(text)
	for position, character := range characters {
		if position > 0 && unicode.IsUpper(character) &&
			(unicode.IsLower(characters[position-1]) ||
				(unicode.IsUpper(characters[position-1]) && position+1 < len(characters) && unicode.IsLower(characters[position+1]))) {
			expanded.WriteByte(' ')
		}
		expanded.WriteRune(unicode.ToLower(character))
	}
	var tokens []string
	for _, token := range strings.FieldsFunc(expanded.String(), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_'
	}) {
		switch token {
		case "a", "an", "the", "to", "for", "of", "in", "on", "with", "and", "or", "please":
			continue
		}
		tokens = append(tokens, token)
		if strings.ContainsRune(token, '_') {
			tokens = append(tokens, strings.FieldsFunc(token, func(character rune) bool { return character == '_' })...)
		}
	}
	return tokens
}

func toolSearchQueryTokens(text string) []string {
	words := strings.Fields(text)
	if len(words) > 1 {
		filtered := words[:0]
		for _, word := range words {
			literal := strings.Trim(word, "\"'`,;:!?()[]{}")
			extension := filepath.Ext(literal)
			if len(extension) > 1 && len(extension) <= 9 && strings.IndexFunc(extension[1:], func(character rune) bool { return !unicode.IsLetter(character) }) < 0 {
				continue
			}
			if literal != "" && strings.IndexFunc(literal, func(character rune) bool { return !unicode.IsDigit(character) }) < 0 {
				continue
			}
			filtered = append(filtered, word)
		}
		text = strings.Join(filtered, " ")
	}
	tokens := toolSearchTokens(text)
	filtered := tokens[:0]
	for _, token := range tokens {
		switch token {
		case "as", "at", "by", "from", "into", "through", "this", "that", "these", "those", "it", "its", "their",
			"you", "your", "me", "my", "our", "we", "can", "could", "would", "should", "do", "does", "did",
			"is", "are", "was", "were", "be", "been", "being", "have", "has", "had", "which", "what", "whether",
			"how", "when", "while", "then", "than", "just", "only", "using", "until":
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

func toolSearchStemKey(token string) string {
	if len(token) < 3 {
		return ""
	}
	for _, character := range token {
		if character < 'a' || character > 'z' {
			return ""
		}
	}
	return "stem:" + english.Stem(token, false)
}

func (field *toolSearchField) addScores(scores map[int]float64, term string, weight float64) {
	postings := field.postings[term]
	idf := math.Log1p((float64(len(field.lengths)-len(postings)) + 0.5) / (float64(len(postings)) + 0.5))
	for _, posting := range postings {
		frequency := float64(posting.frequency)
		normalization := 1.2 * (0.25 + 0.75*float64(field.lengths[posting.document])/field.average)
		scores[posting.document] += weight * idf * frequency * 2.2 / (frequency + normalization)
	}
}

func isDiscoveryTool(name string) bool {
	return name == "tool_search" || name == "tool_describe" || name == "tool_call"
}

func resolveToolSearchName(names []string, requested string) (string, []string) {
	requested = strings.Trim(strings.TrimSpace(requested), "\"'`")
	var candidates []string
	for _, name := range names {
		if name == requested {
			return name, nil
		}
		if strings.EqualFold(name, requested) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	sort.Strings(candidates)
	return "", candidates
}

func (index *toolSearchIndex) search(query string, limit int) map[string]any {
	names := make([]string, len(index.documents))
	byName := make(map[string]int, len(names))
	for position, document := range index.documents {
		names[position], byName[document.name] = document.name, position
	}
	result := map[string]any{
		"query": query, "total_available": len(names), "matches": []map[string]any{},
	}
	var selected []int
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(strings.ToLower(trimmed), "select:") {
		unavailable, truncated := []string{}, []string{}
		ambiguous := make(map[string][]string)
		seen := make(map[string]bool)
		for _, requested := range strings.Split(trimmed[len("select:"):], ",") {
			requested = strings.Trim(strings.TrimSpace(requested), "\"'`")
			if requested == "" {
				continue
			}
			name, candidates := resolveToolSearchName(names, requested)
			if name == "" {
				if len(candidates) > 1 {
					ambiguous[requested] = candidates
				} else {
					unavailable = append(unavailable, requested)
				}
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			if len(selected) >= limit {
				truncated = append(truncated, name)
			} else {
				selected = append(selected, byName[name])
			}
		}
		result["unavailable"], result["ambiguous"], result["truncated"] = unavailable, ambiguous, truncated
		result["total_matches"] = len(seen)
	} else {
		var required []string
		var text []string
		for _, term := range strings.Fields(trimmed) {
			if strings.HasPrefix(term, "+") {
				term = strings.Trim(strings.TrimPrefix(term, "+"), "\"'`,.;:!?()[]{}")
				if term != "" {
					required = append(required, strings.ToLower(term))
				}
			}
			text = append(text, term)
		}
		terms := toolSearchQueryTokens(strings.Join(text, " "))
		scores := make(map[int]float64)
		primaryScores := make(map[int]float64)
		seen := make(map[string]bool)
		for _, term := range terms {
			if seen[term] {
				continue
			}
			seen[term] = true
			index.full.addScores(scores, term, 0.35)
			index.primary.addScores(primaryScores, term, 2)
		}
		queryStems := make(map[string]string, len(terms))
		for _, term := range terms {
			stem := toolSearchStemKey(term)
			queryStems[term] = stem
			if stem != "" && !seen[stem] {
				seen[stem] = true
				index.full.addScores(scores, stem, 0.1225)
				index.primary.addScores(primaryScores, stem, 0.7)
			}
		}
		identifierTerms := make(map[string]string)
		if strings.ContainsRune(trimmed, '_') && len(index.full.postings[strings.ToLower(trimmed)]) == 0 &&
			strings.IndexFunc(trimmed, func(character rune) bool {
				return !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_'
			}) < 0 {
			for _, term := range toolSearchTokens(strings.ReplaceAll(trimmed, "_", " ")) {
				identifierTerms[term] = toolSearchStemKey(term)
			}
		}
		for position, document := range index.documents {
			name := strings.ToLower(document.name)
			allowed := true
			for _, term := range required {
				if !strings.Contains(name, term) {
					allowed = false
					break
				}
			}
			for term, stem := range identifierTerms {
				matched := document.termFrequencies[term] > 0 || document.termFrequencies[stem] > 0
				if !matched && utf8.RuneCountInString(term) >= 4 {
					for _, nameToken := range document.nameTokens {
						if strings.HasPrefix(nameToken, term) {
							matched = true
							break
						}
					}
				}
				if !matched {
					allowed = false
					break
				}
			}
			if !allowed {
				continue
			}
			primaryCoveredTerms, prefixMatches, coveredTerms := 0, 0, 0
			for term := range seen {
				isStem := strings.HasPrefix(term, "stem:")
				if isStem {
					continue
				}
				if document.summaryTerms[term] || document.summaryTerms[queryStems[term]] {
					primaryCoveredTerms++
				}
				matched := document.termFrequencies[term] > 0 || document.termFrequencies[queryStems[term]] > 0
				if utf8.RuneCountInString(term) >= 4 {
					for _, nameToken := range document.nameTokens {
						if nameToken != term && strings.HasPrefix(nameToken, term) {
							prefixMatches++
							matched = true
							break
						}
					}
				}
				if matched {
					coveredTerms++
				}
			}
			scores[position] += float64(prefixMatches)
			coverage := 0.0
			if len(queryStems) > 0 {
				coverage = float64(coveredTerms) / float64(len(queryStems))
				primaryCoverage := float64(primaryCoveredTerms) / float64(len(queryStems))
				scores[position] += primaryScores[position] * primaryCoverage * primaryCoverage
				scores[position] *= coverage
			}
			if name == strings.ToLower(trimmed) {
				scores[position] += 100
			} else if _, matched := queryStems[name]; matched {
				scores[position] += 10 * coverage * coverage
			} else if document.nameStem != "" && seen[document.nameStem] {
				scores[position] += 5 * coverage * coverage
			}
			if scores[position] > 0 || len(required) > 0 {
				selected = append(selected, position)
			}
		}
		sort.Slice(selected, func(left, right int) bool {
			leftID, rightID := selected[left], selected[right]
			if scores[leftID] == scores[rightID] {
				return names[leftID] < names[rightID]
			}
			return scores[leftID] > scores[rightID]
		})
		result["total_matches"] = len(selected)
		if len(selected) > limit {
			selected = selected[:limit]
		}
	}
	matches := make([]map[string]any, 0, len(selected))
	for _, position := range selected {
		document := index.documents[position]
		matches = append(matches, map[string]any{
			"name": document.name, "description": document.description,
			"parameters": append(json.RawMessage(nil), document.parameters...),
		})
		if document.source != nil {
			matches[len(matches)-1]["source"] = *document.source
		}
	}
	result["matches"] = matches
	if len(matches) == 0 {
		result["status"] = "no_matches"
		result["message"] = "No available tools matched. Try English capability keywords, an action name, or select: with exact tool names. Required +terms match tool names only."
	} else {
		result["status"] = "ok"
	}
	return result
}

func limitToolSearchOutput(result map[string]any, budget int) (map[string]any, error) {
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) <= budget {
		return result, err
	}
	matches := result["matches"].([]map[string]any)
	deferred := []map[string]any{}
	result["max_output_bytes"], result["max_supported_bytes"] = budget, maxToolDiscoveryBytes
	result["message"] = "Complete declarations were deferred to fit the output budget. Describe one tool with max_output_bytes >= required_bytes. Do not retry schema_too_large entries; choose another tool or a directly visible interface."
	for len(matches) > 0 && len(encoded) > budget {
		removed := matches[len(matches)-1]
		matches = matches[:len(matches)-1]
		declaration, err := json.Marshal(removed)
		if err != nil {
			return nil, err
		}
		status := "output_budget_exceeded"
		if len(declaration) > maxToolDiscoveryBytes {
			status = "schema_too_large"
		}
		deferred = append([]map[string]any{{"name": removed["name"], "required_bytes": len(declaration), "status": status}}, deferred...)
		result["matches"], result["deferred"] = matches, deferred
		result["status"] = "budget_limited"
		if len(matches) == 0 {
			result["status"] = "output_budget_exceeded"
		}
		encoded, err = json.Marshal(result)
		if err != nil {
			return nil, err
		}
	}
	result["deferred_count"] = len(deferred)
	encoded, err = json.Marshal(result)
	for err == nil && len(encoded) > budget && len(deferred) > 0 {
		deferred = deferred[:len(deferred)-1]
		result["deferred"], result["diagnostics_truncated"] = deferred, true
		encoded, err = json.Marshal(result)
	}
	if err == nil && len(encoded) > budget {
		return map[string]any{"status": "output_budget_exceeded", "matches": []map[string]any{}, "message": "Narrow the query or increase max_output_bytes; even the result diagnostics exceed this budget."}, nil
	}
	return result, err
}

func limitToolDescription(result map[string]any, budget int) (map[string]any, error) {
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) <= budget {
		return result, err
	}
	status := "output_budget_exceeded"
	message := "Retry with max_output_bytes >= required_bytes to receive the complete schema."
	if len(encoded) > maxToolDiscoveryBytes {
		status = "schema_too_large"
		message = "This complete declaration exceeds the supported budget. Do not repeat describe; choose another tool or a directly visible interface."
	}
	diagnostic := map[string]any{
		"status": status, "name": result["name"], "required_bytes": len(encoded),
		"max_output_bytes": budget, "max_supported_bytes": maxToolDiscoveryBytes, "message": message,
	}
	if data, err := json.Marshal(diagnostic); err != nil {
		return nil, err
	} else if len(data) > budget {
		delete(diagnostic, "name")
		diagnostic["diagnostics_truncated"] = true
	}
	return diagnostic, nil
}
