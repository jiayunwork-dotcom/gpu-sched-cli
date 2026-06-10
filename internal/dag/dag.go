package dag

import (
	"fmt"
	"strings"

	"github.com/gpu-sched-cli/internal/model"
)

type DependencyGraph struct {
	edges map[string][]string
}

func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		edges: make(map[string][]string),
	}
}

func (g *DependencyGraph) AddEdge(from, to string) {
	g.edges[from] = append(g.edges[from], to)
}

func (g *DependencyGraph) Dependencies(taskID string) []string {
	return g.edges[taskID]
}

func (g *DependencyGraph) Dependents(taskID string) []string {
	var result []string
	for from, deps := range g.edges {
		for _, dep := range deps {
			if dep == taskID {
				result = append(result, from)
				break
			}
		}
	}
	return result
}

func (g *DependencyGraph) AllNodes() map[string][]string {
	return g.edges
}

func (g *DependencyGraph) Copy() *DependencyGraph {
	ng := NewDependencyGraph()
	for k, v := range g.edges {
		ng.edges[k] = append([]string{}, v...)
	}
	return ng
}

func DetectCycle(g *DependencyGraph) ([]string, bool) {
	white := 0
	gray := 1
	black := 2

	allNodes := make(map[string]bool)
	for from, deps := range g.edges {
		allNodes[from] = true
		for _, dep := range deps {
			allNodes[dep] = true
		}
	}

	color := make(map[string]int)
	parent := make(map[string]string)

	for node := range allNodes {
		color[node] = white
	}

	var cyclePath []string
	found := false

	var dfs func(node string)
	dfs = func(node string) {
		if found {
			return
		}
		color[node] = gray
		for _, dep := range g.edges[node] {
			if found {
				return
			}
			if color[dep] == gray {
				found = true
				cyclePath = []string{dep}
				cur := node
				for cur != dep {
					cyclePath = append([]string{cur}, cyclePath...)
					cur = parent[cur]
				}
				cyclePath = append([]string{dep}, cyclePath...)
				return
			}
			if color[dep] == white {
				parent[dep] = node
				dfs(dep)
			}
		}
		color[node] = black
	}

	for node := range allNodes {
		if color[node] == white {
			dfs(node)
		}
	}

	return cyclePath, found
}

func CascadeSkip(g *DependencyGraph, failedTaskID string, getTask func(string) *model.Task, updateStatus func(string, model.TaskStatus), auditRecord func(model.AuditDecisionType, string, []string, string, map[string]string)) []string {
	visited := make(map[string]bool)
	var skipped []string

	var traverse func(taskID string)
	traverse = func(taskID string) {
		if visited[taskID] {
			return
		}
		visited[taskID] = true
		dependents := g.Dependents(taskID)
		for _, depID := range dependents {
			if visited[depID] {
				continue
			}
			t := getTask(depID)
			if t == nil {
				continue
			}
			if t.Status == model.TaskStatusBlocked || t.Status == model.TaskStatusQueued ||
				t.Status == model.TaskStatusSubmitted {
				updateStatus(depID, model.TaskStatusSkipped)
				skipped = append(skipped, depID)
				if auditRecord != nil {
					auditRecord(model.AuditDecisionSkipped, depID, nil,
						fmt.Sprintf("级联跳过: 依赖任务 %s 失败/取消", failedTaskID),
						map[string]string{
							"failed_dependency": failedTaskID,
						})
				}
				traverse(depID)
			}
		}
	}

	traverse(failedTaskID)
	return skipped
}

type DAGStats struct {
	BlockedCount      int
	AvgDependencyDepth float64
	CriticalPathLen   int
}

func ComputeStats(g *DependencyGraph, getTask func(string) *model.Task) DAGStats {
	blockedCount := 0
	totalDepth := 0
	taskCount := 0
	maxDepth := 0

	allNodes := make(map[string]bool)
	for from, deps := range g.edges {
		allNodes[from] = true
		for _, dep := range deps {
			allNodes[dep] = true
		}
	}

	for node := range allNodes {
		t := getTask(node)
		if t != nil && t.Status == model.TaskStatusBlocked {
			blockedCount++
		}
		depth := computeDepth(g, node)
		totalDepth += depth
		taskCount++
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	avgDepth := 0.0
	if taskCount > 0 {
		avgDepth = float64(totalDepth) / float64(taskCount)
	}

	return DAGStats{
		BlockedCount:      blockedCount,
		AvgDependencyDepth: avgDepth,
		CriticalPathLen:   maxDepth,
	}
}

func computeDepth(g *DependencyGraph, node string) int {
	deps := g.edges[node]
	if len(deps) == 0 {
		return 0
	}
	maxChildDepth := 0
	for _, dep := range deps {
		d := computeDepth(g, dep)
		if d > maxChildDepth {
			maxChildDepth = d
		}
	}
	return maxChildDepth + 1
}

func RenderASCIITree(g *DependencyGraph, getTask func(string) *model.Task, rootTaskID string) string {
	allNodes := make(map[string]bool)
	for from := range g.edges {
		allNodes[from] = true
	}

	var roots []string
	if rootTaskID != "" {
		roots = []string{rootTaskID}
	} else {
		dependent := make(map[string]bool)
		for _, deps := range g.edges {
			for _, dep := range deps {
				dependent[dep] = true
			}
		}
		for node := range allNodes {
			if !dependent[node] {
				roots = append(roots, node)
			}
		}
	}

	var sb strings.Builder
	visited := make(map[string]bool)

	for i, root := range roots {
		if i > 0 {
			sb.WriteString("\n")
		}
		renderNode(&sb, g, getTask, root, "", true, visited)
	}

	return sb.String()
}

func renderNode(sb *strings.Builder, g *DependencyGraph, getTask func(string) *model.Task, nodeID, prefix string, isLast bool, visited map[string]bool) {
	connector := "├── "
	lastConnector := "└── "
	if len(prefix) == 0 {
		connector = ""
		lastConnector = ""
	}

	var selectedConnector string
	if len(prefix) == 0 {
		selectedConnector = ""
	} else if isLast {
		selectedConnector = lastConnector
	} else {
		selectedConnector = connector
	}

	statusStr := ""
	t := getTask(nodeID)
	if t != nil {
		statusStr = fmt.Sprintf(" [%s]", string(t.Status))
	}
	name := nodeID
	if t != nil {
		name = fmt.Sprintf("%s (%s)", t.Spec.Name, nodeID)
	}

	sb.WriteString(prefix + selectedConnector + name + statusStr + "\n")

	if visited[nodeID] {
		sb.WriteString(prefix + childPrefix(isLast, len(prefix) == 0) + "  (see above)\n")
		return
	}
	visited[nodeID] = true

	deps := g.edges[nodeID]
	for i, dep := range deps {
		var childPrefix string
		if len(prefix) == 0 {
			if isLast {
				childPrefix = "    "
			} else {
				childPrefix = "│   "
			}
		} else {
			if isLast {
				childPrefix = prefix + "    "
			} else {
				childPrefix = prefix + "│   "
			}
		}
		isDepLast := i == len(deps)-1
		renderNode(sb, g, getTask, dep, childPrefix, isDepLast, visited)
	}
}

func childPrefix(isLast, isRoot bool) string {
	if isLast {
		return "    "
	}
	return "│   "
}

func RenderSubTree(g *DependencyGraph, getTask func(string) *model.Task, rootTaskID string) string {
	var sb strings.Builder
	t := getTask(rootTaskID)
	rootName := rootTaskID
	statusStr := ""
	if t != nil {
		rootName = fmt.Sprintf("%s (%s)", t.Spec.Name, rootTaskID)
		statusStr = fmt.Sprintf(" [%s]", string(t.Status))
	}
	sb.WriteString(rootName + statusStr + "\n")

	dependents := g.Dependents(rootTaskID)
	if len(dependents) > 0 {
		sb.WriteString("├── dependents (tasks that depend on this):\n")
		for i, depID := range dependents {
			prefix := "│   ├── "
			if i == len(dependents)-1 {
				prefix = "│   └── "
			}
			dt := getTask(depID)
			depName := depID
			depStatus := ""
			if dt != nil {
				depName = fmt.Sprintf("%s (%s)", dt.Spec.Name, depID)
				depStatus = fmt.Sprintf(" [%s]", string(dt.Status))
			}
			sb.WriteString(prefix + depName + depStatus + "\n")
		}
	}

	deps := g.edges[rootTaskID]
	if len(deps) > 0 {
		prefix := "└── depends on:\n"
		if len(dependents) == 0 {
			prefix = "├── depends on:\n"
		}
		sb.WriteString(prefix)
		for i, depID := range deps {
			childPrefix := "    ├── "
			if i == len(deps)-1 {
				childPrefix = "    └── "
			}
			dt := getTask(depID)
			depName := depID
			depStatus := ""
			if dt != nil {
				depName = fmt.Sprintf("%s (%s)", dt.Spec.Name, depID)
				depStatus = fmt.Sprintf(" [%s]", string(dt.Status))
			}
			sb.WriteString(childPrefix + depName + depStatus + "\n")

			subDeps := g.edges[depID]
			for j, subDepID := range subDeps {
				subPrefix := "        ├── "
				if j == len(subDeps)-1 {
					subPrefix = "        └── "
				}
				st := getTask(subDepID)
				subName := subDepID
				subStatus := ""
				if st != nil {
					subName = fmt.Sprintf("%s (%s)", st.Spec.Name, subDepID)
					subStatus = fmt.Sprintf(" [%s]", string(st.Status))
				}
				sb.WriteString(subPrefix + subName + subStatus + "\n")
			}
		}
	}

	return sb.String()
}

func RenderDOT(g *DependencyGraph, getTask func(string) *model.Task) string {
	var sb strings.Builder
	sb.WriteString("digraph DAG {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	allNodes := make(map[string]bool)
	for from, deps := range g.edges {
		allNodes[from] = true
		for _, dep := range deps {
			allNodes[dep] = true
		}
	}

	for node := range allNodes {
		t := getTask(node)
		label := node
		statusStr := ""
		color := "white"
		if t != nil {
			label = fmt.Sprintf("%s\\n(%s)", t.Spec.Name, node)
			statusStr = string(t.Status)
			switch t.Status {
			case model.TaskStatusBlocked:
				color = "lightyellow"
			case model.TaskStatusQueued, model.TaskStatusSubmitted:
				color = "lightblue"
			case model.TaskStatusRunning:
				color = "lightgreen"
			case model.TaskStatusCompleted:
				color = "grey90"
			case model.TaskStatusFailed, model.TaskStatusTimedOut:
				color = "lightcoral"
			case model.TaskStatusSkipped:
				color = "lightgrey"
			}
		}
		sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", fillcolor=%s, style=\"rounded,filled\"];\n", node, label, color))
		_ = statusStr
	}

	sb.WriteString("\n")

	for from, deps := range g.edges {
		for _, dep := range deps {
			sb.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\";\n", from, dep))
		}
	}

	sb.WriteString("}\n")
	return sb.String()
}
