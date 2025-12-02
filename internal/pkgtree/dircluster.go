package pkgtree

import (
	"sort"
	"strings"

	"github.com/loov/goda/internal/pkggraph"
)

// DirCluster represents a directory-based cluster node
type DirCluster struct {
	Path         string
	Parent       *DirCluster
	Children     []*DirCluster
	Packages     []*Package
	Depth        int
	sortedChildren []string
	childrenMap    map[string]*DirCluster
}

// NewDirCluster creates a new directory cluster
func NewDirCluster(path string, parent *DirCluster, depth int) *DirCluster {
	return &DirCluster{
		Path:        path,
		Parent:      parent,
		Depth:       depth,
		childrenMap: make(map[string]*DirCluster),
	}
}

// AddPackage adds a package to this cluster
func (dc *DirCluster) AddPackage(pkg *Package) {
	dc.Packages = append(dc.Packages, pkg)
}

// GetOrCreateChild gets or creates a child cluster
func (dc *DirCluster) GetOrCreateChild(name string, depth int) *DirCluster {
	if child, ok := dc.childrenMap[name]; ok {
		return child
	}

	childPath := name
	if dc.Path != "" {
		childPath = dc.Path + "/" + name
	}

	child := NewDirCluster(childPath, dc, depth)
	dc.childrenMap[name] = child
	dc.sortedChildren = append(dc.sortedChildren, name)
	dc.Children = append(dc.Children, child)
	return child
}

// Sort sorts children by name
func (dc *DirCluster) Sort() {
	sort.Strings(dc.sortedChildren)

	// Resort Children slice based on sorted names
	sortedChildren := make([]*DirCluster, 0, len(dc.Children))
	for _, name := range dc.sortedChildren {
		sortedChildren = append(sortedChildren, dc.childrenMap[name])
	}
	dc.Children = sortedChildren

	// Recursively sort children
	for _, child := range dc.Children {
		child.Sort()
	}
}

// ClusterByDirectory creates directory-based clusters from a tree
func ClusterByDirectory(t *Tree, basePackage string, maxDepth int) *DirCluster {
	root := NewDirCluster("", nil, 0)

	// Build lookup table for packages
	lookup := t.LookupTable()

	// Process all packages
	for _, repo := range t.Repos {
		// Process packages in modules
		for _, mod := range repo.Modules {
			for _, pkg := range mod.Pkgs {
				addPackageToCluster(root, pkg, basePackage, maxDepth)
			}
		}

		// Process packages directly in repo
		for _, pkg := range repo.Pkgs {
			addPackageToCluster(root, pkg, basePackage, maxDepth)
		}
	}

	_ = lookup // Keep for future use
	root.Sort()
	return root
}

// addPackageToCluster adds a package to the appropriate cluster based on directory structure
func addPackageToCluster(root *DirCluster, pkg *Package, basePackage string, maxDepth int) {
	pkgPath := pkg.GraphNode.ID

	// Remove base package prefix if present
	relativePath := pkgPath
	if basePackage != "" && strings.HasPrefix(pkgPath, basePackage+"/") {
		relativePath = strings.TrimPrefix(pkgPath, basePackage+"/")
	} else if pkgPath == basePackage {
		// Root package
		root.AddPackage(pkg)
		return
	}

	if relativePath == "" || relativePath == pkgPath {
		// If we couldn't make it relative, just use the full path
		relativePath = pkgPath
	}

	// Split by directory
	parts := strings.Split(relativePath, "/")

	// Apply depth limit if specified
	if maxDepth > 0 && len(parts) > maxDepth {
		parts = parts[:maxDepth]
	}

	// Navigate/create the cluster hierarchy
	current := root
	for i, part := range parts {
		depth := i + 1
		if maxDepth > 0 && depth > maxDepth {
			break
		}
		current = current.GetOrCreateChild(part, depth)
	}

	// Add package to the deepest cluster
	current.AddPackage(pkg)
}

// GetBasePackage determines the base package from a graph
func GetBasePackage(graph *pkggraph.Graph) string {
	if len(graph.Sorted) == 0 {
		return ""
	}

	basePackage := graph.Sorted[0].ID
	for _, node := range graph.Sorted {
		if len(node.ID) < len(basePackage) {
			basePackage = node.ID
		}
	}

	// Find common prefix
	if len(graph.Sorted) > 1 {
		commonPrefix := basePackage
		for _, node := range graph.Sorted[1:] {
			commonPrefix = longestCommonPrefix(commonPrefix, node.ID)
			if commonPrefix == "" {
				break
			}
		}
		// Trim to last complete path component
		if idx := strings.LastIndex(commonPrefix, "/"); idx > 0 {
			commonPrefix = commonPrefix[:idx]
		}
		if commonPrefix != "" {
			basePackage = commonPrefix
		}
	}

	return basePackage
}

// longestCommonPrefix finds the longest common prefix between two strings
func longestCommonPrefix(a, b string) string {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:minLen]
}
