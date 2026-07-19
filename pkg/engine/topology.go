package engine

import (
	"sort"
	"strings"

	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func pathContains(parent, child string) bool {
	return parent != child && strings.HasPrefix(child, parent+"/")
}

func pathDepth(path string) int { return strings.Count(path, "/") + 1 }

func relativeComponentPath(parent, child string) string {
	return strings.TrimPrefix(child, parent+"/")
}

func directResolvedChildren(parent protocol.ResolvedRef, components []protocol.ResolvedRef, revisions map[string]string) []protocol.ChildComponent {
	var children []protocol.ChildComponent
	for _, candidate := range components {
		if !pathContains(parent.Path, candidate.Path) {
			continue
		}
		direct := true
		for _, middle := range components {
			if middle.ID != parent.ID && middle.ID != candidate.ID && pathContains(parent.Path, middle.Path) && pathContains(middle.Path, candidate.Path) {
				direct = false
				break
			}
		}
		if !direct {
			continue
		}
		revision := candidate.Revision
		if revisions != nil && revisions[candidate.ID] != "" {
			revision = revisions[candidate.ID]
		}
		children = append(children, protocol.ChildComponent{ID: candidate.ID, Path: relativeComponentPath(parent.Path, candidate.Path), Provider: candidate.Provider, Revision: revision})
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Path < children[j].Path })
	return children
}

func directArtifactChildren(parent spec.Component, components []spec.Component) []protocol.ChildComponent {
	refs := make([]protocol.ResolvedRef, 0, len(components))
	revisions := make(map[string]string, len(components))
	for _, component := range components {
		refs = append(refs, protocol.ResolvedRef{ID: component.ID, Path: component.Path, Provider: component.Provider})
		revisions[component.ID] = componentRevision(component)
	}
	return directResolvedChildren(protocol.ResolvedRef{ID: parent.ID, Path: parent.Path}, refs, revisions)
}

func componentRevision(component spec.Component) string {
	if component.Reference != nil {
		return component.Reference.Revision
	}
	if component.Embedded != nil {
		return component.Embedded.Revision
	}
	return ""
}

func sortedResolved(components []protocol.ResolvedRef, parentFirst bool) []protocol.ResolvedRef {
	out := append([]protocol.ResolvedRef(nil), components...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := pathDepth(out[i].Path), pathDepth(out[j].Path)
		if di == dj {
			return out[i].Path < out[j].Path
		}
		if parentFirst {
			return di < dj
		}
		return di > dj
	})
	return out
}

func sortedArtifact(components []spec.Component, parentFirst bool) []spec.Component {
	out := append([]spec.Component(nil), components...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := pathDepth(out[i].Path), pathDepth(out[j].Path)
		if di == dj {
			return out[i].Path < out[j].Path
		}
		if parentFirst {
			return di < dj
		}
		return di > dj
	})
	return out
}
