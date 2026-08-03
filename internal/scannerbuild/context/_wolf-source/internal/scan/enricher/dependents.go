package enricher

// FindDependents returns files that depend on the given file, up to maxDepth
// levels of transitive dependents. Results are capped at 20.
func FindDependents(graph *ImportGraph, filePath string, maxDepth int) []string {
	if graph == nil || len(graph.ImportedBy) == 0 {
		return nil
	}

	const maxResults = 20

	visited := make(map[string]bool)
	visited[filePath] = true // don't include self

	var result []string
	current := []string{filePath}

	for depth := 0; depth < maxDepth && len(current) > 0 && len(result) < maxResults; depth++ {
		var next []string
		for _, f := range current {
			for _, dep := range graph.ImportedBy[f] {
				if !visited[dep] {
					visited[dep] = true
					result = append(result, dep)
					next = append(next, dep)
					if len(result) >= maxResults {
						return result
					}
				}
			}
		}
		current = next
	}

	return result
}
