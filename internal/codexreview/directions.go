package codexreview

import (
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type directionDefinition struct {
	direction quality.ReviewDirection
	keywords  []string
}

var directionCatalog = []directionDefinition{
	{
		direction: quality.ReviewDirection{
			ID:     "security-boundaries",
			Prompt: "Check trust boundaries, authorization, validation, and secret handling around the changed behavior.",
		},
		keywords: []string{"auth", "token", "permission", "role", "tenant", "secret", "credential", "encrypt", "sanitize", "csrf", "xss"},
	},
	{
		direction: quality.ReviewDirection{
			ID:     "data-business-correctness",
			Prompt: "Trace value, state, identity, ordering, and precision changes through their real business effects.",
		},
		keywords: []string{"amount", "price", "balance", "payment", "settlement", "order", "transaction", "precision", "decimal", "cursor", "pagination", "state", "status", "idempot", "dedup"},
	},
	{
		direction: quality.ReviewDirection{
			ID:     "reliability-lifecycle",
			Prompt: "Trace errors, timeouts, retries, cancellation, concurrency, and resource lifecycle across the changed path.",
		},
		keywords: []string{"timeout", "time.after", "context", "cancel", "retry", "close", "goroutine", "channel", "mutex", "lock", "queue", "worker", "shutdown"},
	},
	{
		direction: quality.ReviewDirection{
			ID:     "contracts-rollout",
			Prompt: "Check whether changed interfaces, schemas, configuration, migrations, and rollout order remain compatible.",
		},
		keywords: []string{"api", "route", "handler", "protocol", "schema", "migration", "config", "docker", "deploy", "openapi", "proto", "version", "compat"},
	},
	{
		direction: quality.ReviewDirection{
			ID:     "scale-side-effects",
			Prompt: "Follow remote calls, storage access, loops, batching, caches, and side effects for production-scale failure modes.",
		},
		keywords: []string{"batch", "scan", "query", "sql", "database", "rpc", "http", "cache", "index", "history", "backfill", "sync", "cron", "loop"},
	},
}

var fallbackDirection = quality.ReviewDirection{
	ID:     "behavioral-correctness",
	Prompt: "Trace the changed behavior end to end and look for concrete correctness or production failure modes.",
}

type scoredDirection struct {
	index int
	score int
}

func selectDirections(request quality.ReviewRequest, diff []byte) []quality.ReviewDirection {
	var source strings.Builder
	for _, path := range request.ChangedFiles {
		source.WriteString(strings.ToLower(path))
		source.WriteByte('\n')
	}
	for _, line := range strings.Split(strings.ToLower(string(diff)), "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			source.WriteString(line[1:])
			source.WriteByte('\n')
		}
	}
	haystack := source.String()
	scored := make([]scoredDirection, 0, len(directionCatalog))
	for index, definition := range directionCatalog {
		score := 0
		for _, keyword := range definition.keywords {
			if count := strings.Count(haystack, keyword); count > 0 {
				if count > 3 {
					count = 3
				}
				score += count
			}
		}
		if score > 0 {
			scored = append(scored, scoredDirection{index: index, score: score})
		}
	}
	if len(scored) == 0 {
		return []quality.ReviewDirection{fallbackDirection}
	}
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].score != scored[right].score {
			return scored[left].score > scored[right].score
		}
		return scored[left].index < scored[right].index
	})
	if len(scored) > 3 {
		scored = scored[:3]
	}
	result := make([]quality.ReviewDirection, 0, len(scored))
	for _, item := range scored {
		result = append(result, directionCatalog[item.index].direction)
	}
	return result
}
