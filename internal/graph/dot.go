package graph

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/template"

	"github.com/loov/goda/internal/pkggraph"
	"github.com/loov/goda/internal/pkgtree"
)

type Dot struct {
	out io.Writer
	err io.Writer

	docs          string
	clusters      bool
	clusterByDir  bool
	clusterDepth  int
	clusterColors bool
	nocolor       bool
	shortID       bool

	label *template.Template
}

// Color palette for different nesting levels (from cluster_graph.py)
var clusterBgColors = []string{
	"#E8F4F8", // Light blue
	"#D4E6F1", // Medium light blue
	"#AED6F1", // Medium blue
	"#85C1E9", // Darker blue
	"#5DADE2", // Even darker blue
	"#3498DB", // Deep blue
	"#2E86C1", // Deeper blue
	"#2874A6", // Very deep blue
}

// Border colors for clusters
var clusterBorderColors = []string{
	"#5DADE2", // Blue
	"#3498DB", // Darker blue
	"#2E86C1", // Even darker
	"#1F618D", // Deep
	"#154360", // Very deep
}

func (ctx *Dot) Label(p *pkggraph.Node) string {
	var labelText strings.Builder
	err := ctx.label.Execute(&labelText, p)
	if err != nil {
		fmt.Fprintf(ctx.err, "template error: %v\n", err)
	}
	return labelText.String()
}

func (ctx *Dot) ModuleLabel(mod *pkgtree.Module) string {
	lbl := mod.Mod.Path
	if mod.Mod.Version != "" {
		lbl += "@" + mod.Mod.Version
	}
	if mod.Local {
		lbl += " (local)"
	}
	if rep := mod.Mod.Replace; rep != nil {
		lbl += " =>\\n" + rep.Path
		if rep.Version != "" {
			lbl += "@" + rep.Version
		}
	}
	return lbl
}

func (ctx *Dot) TreePackageLabel(tp *pkgtree.Package, parentPrinted bool) string {
	suffix := ""
	parentPath := tp.Parent.Path()
	if parentPrinted && tp.Parent != nil && parentPath != "" {
		suffix = strings.TrimPrefix(tp.Path(), parentPath+"/")
	}

	if suffix != "" && ctx.shortID {
		defer func(previousID string) { tp.GraphNode.ID = previousID }(tp.GraphNode.ID)
		tp.GraphNode.ID = suffix
	}

	var labelText strings.Builder
	err := ctx.label.Execute(&labelText, tp.GraphNode)
	if err != nil {
		fmt.Fprintf(ctx.err, "template error: %v\n", err)
	}
	return labelText.String()
}

func (ctx *Dot) RepoRef(repo *pkgtree.Repo) string {
	return fmt.Sprintf(`href=%q`, ctx.docs+repo.Path())
}

func (ctx *Dot) ModuleRef(mod *pkgtree.Module) string {
	return fmt.Sprintf(`href=%q`, ctx.docs+mod.Path()+"@"+mod.Mod.Version)
}

func (ctx *Dot) TreePackageRef(tp *pkgtree.Package) string {
	return fmt.Sprintf(`href=%q`, ctx.docs+tp.Path())
}

func (ctx *Dot) Ref(p *pkggraph.Node) string {
	return fmt.Sprintf(`href=%q`, ctx.docs+p.ID)
}

// getClusterColorsForDepth returns background and border colors for a cluster at given depth
func (ctx *Dot) getClusterColorsForDepth(depth int) (bg, border string) {
	if !ctx.clusterColors || ctx.nocolor {
		return "", ""
	}
	bg = clusterBgColors[depth%len(clusterBgColors)]
	border = clusterBorderColors[depth%len(clusterBorderColors)]
	return bg, border
}

func (ctx *Dot) writeGraphProperties() {
	if ctx.nocolor {
		fmt.Fprintf(ctx.out, "    node [fontsize=10 shape=rectangle target=\"_graphviz\"];\n")
		fmt.Fprintf(ctx.out, "    edge [tailport=e];\n")
	} else {
		fmt.Fprintf(ctx.out, "    node [penwidth=2 fontsize=10 shape=rectangle target=\"_graphviz\"];\n")
		fmt.Fprintf(ctx.out, "    edge [tailport=e penwidth=2];\n")
	}
	fmt.Fprintf(ctx.out, "    compound=true;\n")

	fmt.Fprintf(ctx.out, "    rankdir=LR;\n")
	fmt.Fprintf(ctx.out, "    newrank=true;\n")
	fmt.Fprintf(ctx.out, "    ranksep=\"1.5\";\n")
	fmt.Fprintf(ctx.out, "    quantum=\"0.5\";\n")
}

func (ctx *Dot) Write(graph *pkggraph.Graph) error {
	if ctx.clusters {
		if ctx.clusterByDir {
			return ctx.WriteDirectoryClusters(graph)
		}
		return ctx.WriteClusters(graph)
	} else {
		return ctx.WriteRegular(graph)
	}
}

func (ctx *Dot) WriteRegular(graph *pkggraph.Graph) error {
	fmt.Fprintf(ctx.out, "digraph G {\n")
	ctx.writeGraphProperties()
	defer fmt.Fprintf(ctx.out, "}\n")

	for _, n := range graph.Sorted {
		fmt.Fprintf(ctx.out, "    %v [label=\"%v\" %v %v];\n", pkgID(n), ctx.Label(n), ctx.Ref(n), ctx.colorOf(n))
	}

	for _, src := range graph.Sorted {
		for _, dst := range src.ImportsNodes {
			fmt.Fprintf(ctx.out, "    %v -> %v [%v];\n", pkgID(src), pkgID(dst), ctx.colorOf(dst))
		}
	}

	return nil
}

func (ctx *Dot) WriteClusters(graph *pkggraph.Graph) error {
	root, err := pkgtree.From(graph)
	if err != nil {
		return fmt.Errorf("failed to construct cluster tree: %v", err)
	}
	lookup := root.LookupTable()
	isCluster := map[*pkggraph.Node]bool{}

	fmt.Fprintf(ctx.out, "digraph G {\n")
	ctx.writeGraphProperties()
	defer fmt.Fprintf(ctx.out, "}\n")

	printed := make(map[pkgtree.Node]bool)

	var visit func(tn pkgtree.Node)
	visit = func(tn pkgtree.Node) {
		switch tn := tn.(type) {
		case *pkgtree.Repo:
			if tn.SameAsOnlyModule() {
				break
			}
			printed[tn] = true
			fmt.Fprintf(ctx.out, "subgraph %q {\n", "cluster_"+tn.Path())
			fmt.Fprintf(ctx.out, "    label=\"%v\"\n", tn.Path())
			fmt.Fprintf(ctx.out, "    tooltip=\"%v\"\n", tn.Path())
			fmt.Fprintf(ctx.out, "    %v\n", ctx.RepoRef(tn))
			defer fmt.Fprintf(ctx.out, "}\n")

		case *pkgtree.Module:
			printed[tn] = true
			label := ctx.ModuleLabel(tn)
			fmt.Fprintf(ctx.out, "subgraph %q {\n", "cluster_"+tn.Path())
			fmt.Fprintf(ctx.out, "    label=\"%v\"\n", label)
			fmt.Fprintf(ctx.out, "    tooltip=\"%v\"\n", label)
			fmt.Fprintf(ctx.out, "    %v\n", ctx.ModuleRef(tn))
			defer fmt.Fprintf(ctx.out, "}\n")

		case *pkgtree.Package:
			printed[tn] = true
			gn := tn.GraphNode
			if tn.Path() == tn.Parent.Path() {
				isCluster[tn.GraphNode] = true
				shape := "circle"
				if tn.OnlyChild() {
					shape = "point"
				}
				fmt.Fprintf(ctx.out, "    %v [label=\"\" tooltip=\"%v\" shape=%v %v rank=0];\n", pkgID(gn), tn.Path(), shape, ctx.colorOf(gn))
			} else {
				label := ctx.TreePackageLabel(tn, printed[tn.Parent])
				href := ctx.TreePackageRef(tn)
				fmt.Fprintf(ctx.out, "    %v [label=\"%v\" tooltip=\"%v\" %v %v];\n", pkgID(gn), label, tn.Path(), href, ctx.colorOf(gn))
			}
		}

		tn.VisitChildren(visit)
	}
	root.VisitChildren(visit)

	for _, src := range graph.Sorted {
		srctree := lookup[src]
		for _, dst := range src.ImportsNodes {
			dstID := pkgID(dst)
			dstTree := lookup[dst]
			tooltip := src.ID + " -> " + dst.ID

			if isCluster[dst] && srctree.Parent != dstTree {
				fmt.Fprintf(ctx.out, "    %v -> %v [tooltip=\"%v\" lhead=%q %v];\n", pkgID(src), dstID, tooltip, "cluster_"+dst.ID, ctx.colorOf(dst))
			} else {
				fmt.Fprintf(ctx.out, "    %v -> %v [tooltip=\"%v\" %v];\n", pkgID(src), dstID, tooltip, ctx.colorOf(dst))
			}
		}
	}

	return nil
}

func (ctx *Dot) colorOf(p *pkggraph.Node) string {
	if p.Color != "" {
		return "color=" + strconv.Quote(p.Color)
	}
	if ctx.nocolor {
		return ""
	}

	hash := sha256.Sum256([]byte(p.PkgPath))
	hue := float64(uint(hash[0])<<8|uint(hash[1])) / 0xFFFF
	return "color=\"" + hslahex(hue, 0.9, 0.3, 0.7) + "\""
}

// WriteDirectoryClusters writes the graph with directory-based nested clusters
func (ctx *Dot) WriteDirectoryClusters(graph *pkggraph.Graph) error {
	root, err := pkgtree.From(graph)
	if err != nil {
		return fmt.Errorf("failed to construct cluster tree: %v", err)
	}

	// Get base package for relative path calculation
	basePackage := pkgtree.GetBasePackage(graph)

	// Build directory cluster structure
	dirRoot := pkgtree.ClusterByDirectory(root, basePackage, ctx.clusterDepth)

	fmt.Fprintf(ctx.out, "digraph G {\n")
	ctx.writeGraphProperties()
	defer fmt.Fprintf(ctx.out, "}\n")

	// Write root packages (if any)
	if len(dirRoot.Packages) > 0 {
		fmt.Fprintf(ctx.out, "    // Root packages\n")
		for _, pkg := range dirRoot.Packages {
			gn := pkg.GraphNode
			label := ctx.Label(gn)
			href := ctx.Ref(gn)
			fmt.Fprintf(ctx.out, "    %v [label=\"%v\" %v %v];\n", pkgID(gn), label, href, ctx.colorOf(gn))
		}
		fmt.Fprintf(ctx.out, "\n")
	}

	// Write directory clusters recursively
	for _, child := range dirRoot.Children {
		ctx.writeDirCluster(child, "    ")
	}

	// Write edges
	fmt.Fprintf(ctx.out, "    // Edges\n")
	for _, src := range graph.Sorted {
		for _, dst := range src.ImportsNodes {
			tooltip := src.ID + " -> " + dst.ID
			fmt.Fprintf(ctx.out, "    %v -> %v [tooltip=\"%v\" %v];\n", pkgID(src), pkgID(dst), tooltip, ctx.colorOf(dst))
		}
	}

	return nil
}

// writeDirCluster writes a directory cluster and its nested subclusters
func (ctx *Dot) writeDirCluster(dc *pkgtree.DirCluster, indent string) {
	// Get colors based on depth
	bgColor, borderColor := ctx.getClusterColorsForDepth(dc.Depth - 1)

	// Create cluster name from path
	clusterName := "cluster_" + strings.ReplaceAll(strings.ReplaceAll(dc.Path, "/", "_"), ".", "_")

	// Get the display name (last component of path)
	displayName := dc.Path
	if idx := strings.LastIndex(dc.Path, "/"); idx >= 0 {
		displayName = dc.Path[idx+1:]
	}

	fmt.Fprintf(ctx.out, "%ssubgraph %q {\n", indent, clusterName)
	fmt.Fprintf(ctx.out, "%s    label=\"%s\";\n", indent, displayName)
	fmt.Fprintf(ctx.out, "%s    style=filled;\n", indent)

	if bgColor != "" {
		fmt.Fprintf(ctx.out, "%s    fillcolor=\"%s\";\n", indent, bgColor)
	}
	if borderColor != "" {
		fmt.Fprintf(ctx.out, "%s    color=\"%s\";\n", indent, borderColor)
	}

	fmt.Fprintf(ctx.out, "%s    penwidth=2;\n", indent)

	// Adjust font size based on depth
	fontSize := 14 - dc.Depth
	if fontSize < 8 {
		fontSize = 8
	}
	fmt.Fprintf(ctx.out, "%s    fontsize=%d;\n", indent, fontSize)
	fmt.Fprintf(ctx.out, "%s    fontname=\"Helvetica-Bold\";\n", indent)

	// Set node defaults for packages within this cluster
	if !ctx.nocolor {
		fmt.Fprintf(ctx.out, "%s    node [style=filled,fillcolor=white];\n", indent)
	}

	// Add margin for better spacing if there are nested clusters
	if len(dc.Children) > 0 {
		fmt.Fprintf(ctx.out, "%s    margin=20;\n", indent)
	}

	fmt.Fprintf(ctx.out, "\n")

	// Write packages directly in this cluster
	if len(dc.Packages) > 0 {
		for _, pkg := range dc.Packages {
			gn := pkg.GraphNode
			label := ctx.Label(gn)
			href := ctx.Ref(gn)
			fmt.Fprintf(ctx.out, "%s    %v [label=\"%v\" %v %v];\n", indent, pkgID(gn), label, href, ctx.colorOf(gn))
		}
		if len(dc.Children) > 0 {
			fmt.Fprintf(ctx.out, "\n")
		}
	}

	// Write nested subclusters
	for _, child := range dc.Children {
		ctx.writeDirCluster(child, indent+"    ")
	}

	fmt.Fprintf(ctx.out, "%s}\n\n", indent)
}
