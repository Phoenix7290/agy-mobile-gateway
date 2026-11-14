package runner

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanUserRequest strips AGY <USER_REQUEST> wrapper tags from transcript text.
func CleanUserRequest(text string) string {
	cleaned := text
	if idx := strings.Index(cleaned, "<USER_REQUEST>"); idx >= 0 {
		cleaned = cleaned[idx+len("<USER_REQUEST>"):]
	}
	if idx := strings.Index(cleaned, "</USER_REQUEST>"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	return strings.TrimSpace(cleaned)
}

// ExtractConversationTitle picks the first meaningful line, skipping attachment wrapper notices.
func ExtractConversationTitle(text, fallback string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 55
	}
	cleaned := CleanUserRequest(text)
	attachFallback := ""
	for _, rawLine := range strings.Split(cleaned, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[Arquivos anexados pelo usuário em ") {
			if attachFallback == "" {
				if _, afterColon, ok := strings.Cut(line, ":"); ok {
					filesPart := strings.SplitN(afterColon, ". Inspecione-os", 2)[0]
					filesPart = strings.TrimSuffix(strings.TrimSpace(filesPart), "]")
					var names []string
					for _, p := range strings.Split(filesPart, ",") {
						p = strings.TrimSpace(p)
						if p != "" {
							names = append(names, filepath.Base(p))
						}
					}
					if len(names) > 0 {
						attachFallback = "Anexo: " + strings.Join(names, ", ")
					}
				}
			}
			continue
		}
		return truncateRunes(line, maxLen)
	}
	if attachFallback != "" {
		return truncateRunes(attachFallback, maxLen)
	}
	return fallback
}

func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

func loadSummaryTitles(cliDir string) map[string]string {
	out := map[string]string{}
	dbPath := filepath.Join(cliDir, "conversation_summaries.db")
	if _, err := os.Stat(dbPath); err != nil {
		return out
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(1000)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return out
	}
	defer conn.Close()

	rows, err := conn.Query("SELECT conversation_id, title FROM conversation_summaries WHERE title != ''")
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var cid, title string
		if err := rows.Scan(&cid, &title); err == nil && cid != "" && strings.TrimSpace(title) != "" {
			out[cid] = strings.TrimSpace(title)
		}
	}
	return out
}
