package axuiautomation

import (
	"strings"
)

// TraversalMode specifies how to traverse the element tree.
type TraversalMode int

const (
	// BFS performs breadth-first search (default).
	BFS TraversalMode = iota
	// DFS performs depth-first search.
	DFS
)

// ElementPredicate is a function that tests if an element matches criteria.
type ElementPredicate func(*Element) bool

// ElementQuery provides a fluent API for finding UI elements.
type ElementQuery struct {
	root       *Element
	app        *Application
	predicates []ElementPredicate
	maxVisit   int
	traversal  TraversalMode
}

// newElementQuery creates a new element query.
func newElementQuery(root *Element, app *Application) *ElementQuery {
	return &ElementQuery{
		root:       root,
		app:        app,
		predicates: make([]ElementPredicate, 0),
		maxVisit:   5000, // Default limit
		traversal:  BFS,
	}
}

// clone creates a copy of the query with a new predicate slice.
func (q *ElementQuery) clone() *ElementQuery {
	return &ElementQuery{
		root:       q.root,
		app:        q.app,
		predicates: append([]ElementPredicate{}, q.predicates...),
		maxVisit:   q.maxVisit,
		traversal:  q.traversal,
	}
}

// Matching adds a custom predicate to the query.
func (q *ElementQuery) Matching(pred ElementPredicate) *ElementQuery {
	nq := q.clone()
	nq.predicates = append(nq.predicates, pred)
	return nq
}

// ByRole filters elements by their accessibility role.
func (q *ElementQuery) ByRole(role string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.Role() == role
	})
}

// ByTitle filters elements by their title.
func (q *ElementQuery) ByTitle(title string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.Title() == title
	})
}

// ByTitleContains filters elements whose title contains the given substring.
func (q *ElementQuery) ByTitleContains(substr string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return strings.Contains(e.Title(), substr)
	})
}

// ByTitlePrefix filters elements whose title starts with the given prefix.
func (q *ElementQuery) ByTitlePrefix(prefix string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return strings.HasPrefix(e.Title(), prefix)
	})
}

// ByIdentifier filters elements by their unique identifier.
func (q *ElementQuery) ByIdentifier(id string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.Identifier() == id
	})
}

// ByDescription filters elements by their description.
func (q *ElementQuery) ByDescription(desc string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.Description() == desc
	})
}

// ByValue filters elements by their value.
func (q *ElementQuery) ByValue(value string) *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.Value() == value
	})
}

// Enabled filters to only enabled elements.
func (q *ElementQuery) Enabled() *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.IsEnabled()
	})
}

// Focused filters to only focused elements.
func (q *ElementQuery) Focused() *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.IsFocused()
	})
}

// Selected filters to only selected elements.
func (q *ElementQuery) Selected() *ElementQuery {
	return q.Matching(func(e *Element) bool {
		return e.IsSelected()
	})
}

// WithLimit sets the maximum number of elements to visit during search.
func (q *ElementQuery) WithLimit(n int) *ElementQuery {
	nq := q.clone()
	nq.maxVisit = n
	return nq
}

// WithTraversal sets the traversal mode (BFS or DFS).
func (q *ElementQuery) WithTraversal(mode TraversalMode) *ElementQuery {
	nq := q.clone()
	nq.traversal = mode
	return nq
}

// matches checks if an element matches all predicates.
func (q *ElementQuery) matches(e *Element) bool {
	for _, pred := range q.predicates {
		if !pred(e) {
			return false
		}
	}
	return true
}

// Element returns the element at the given index (0 for first match).
func (q *ElementQuery) Element(index int) *Element {
	elements := q.allElementsInternal(index + 1)
	if index < len(elements) {
		return elements[index]
	}
	return nil
}

// First returns the first matching element.
func (q *ElementQuery) First() *Element {
	return q.Element(0)
}

// AllElements returns all matching elements.
func (q *ElementQuery) AllElements() []*Element {
	return q.allElementsInternal(-1)
}

// Count returns the number of matching elements.
func (q *ElementQuery) Count() int {
	return len(q.AllElements())
}

// Exists returns true if at least one matching element exists.
func (q *ElementQuery) Exists() bool {
	return q.First() != nil
}

// allElementsInternal performs the actual search.
// If limit > 0, stops after finding that many matches.
// If limit < 0, finds all matches.
func (q *ElementQuery) allElementsInternal(limit int) []*Element {
	if q.root == nil || q.root.ref == 0 {
		return nil
	}

	var results []*Element
	visited := 0

	if q.traversal == DFS {
		results = q.searchDFS(q.root, limit, &visited)
	} else {
		results = q.searchBFS(q.root, limit, &visited)
	}

	return results
}

// searchBFS performs breadth-first search.
func (q *ElementQuery) searchBFS(root *Element, limit int, visited *int) []*Element {
	var results []*Element
	queue := []*Element{root}

	for len(queue) > 0 && *visited < q.maxVisit {
		current := queue[0]
		queue = queue[1:]

		if current == nil {
			continue
		}

		*visited++

		// Check if this element matches
		if q.matches(current) {
			results = append(results, current)
			if limit > 0 && len(results) >= limit {
				// Release remaining elements in queue
				for _, e := range queue {
					if e != root {
						e.Release()
					}
				}
				return results
			}
		} else if current != root {
			// Release non-matching elements that aren't the root
			defer current.Release()
		}

		// Add children to queue
		children := current.Children()
		queue = append(queue, children...)
	}

	return results
}

// searchDFS performs depth-first search.
func (q *ElementQuery) searchDFS(root *Element, limit int, visited *int) []*Element {
	var results []*Element
	q.searchDFSRecursive(root, limit, visited, &results, root)
	return results
}

func (q *ElementQuery) searchDFSRecursive(e *Element, limit int, visited *int, results *[]*Element, root *Element) bool {
	if e == nil || *visited >= q.maxVisit {
		return false
	}

	*visited++

	// Check if this element matches
	if q.matches(e) {
		*results = append(*results, e)
		if limit > 0 && len(*results) >= limit {
			return true // Stop searching
		}
	} else if e != root {
		defer e.Release()
	}

	// Search children
	children := e.Children()
	for _, child := range children {
		if q.searchDFSRecursive(child, limit, visited, results, root) {
			// Release remaining children
			return true
		}
	}

	return false
}

// ForEach calls the given function for each matching element.
// The function can return false to stop iteration.
func (q *ElementQuery) ForEach(fn func(*Element) bool) {
	if q.root == nil || q.root.ref == 0 {
		return
	}

	visited := 0
	q.forEachBFS(q.root, fn, &visited)
}

func (q *ElementQuery) forEachBFS(root *Element, fn func(*Element) bool, visited *int) {
	queue := []*Element{root}

	for len(queue) > 0 && *visited < q.maxVisit {
		current := queue[0]
		queue = queue[1:]

		if current == nil {
			continue
		}

		*visited++

		// Check if this element matches
		if q.matches(current) {
			if !fn(current) {
				// User wants to stop
				for _, e := range queue {
					if e != root {
						e.Release()
					}
				}
				return
			}
		}

		// Release non-root elements after checking
		if current != root {
			defer current.Release()
		}

		// Add children to queue
		children := current.Children()
		queue = append(queue, children...)
	}
}
