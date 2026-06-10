package dag

import (
	"fmt"
	"strings"
	"time"

	"github.com/gpu-sched-cli/internal/model"
)

type DepCondition string

const (
	DepConditionCompleted    DepCondition = "completed"
	DepConditionSuccessOrSkip DepCondition = "success_or_skip"
	DepConditionAnyTerminal  DepCondition = "any_terminal"
)

type DependencyEdge struct {
	From      string
	To        string
	Condition DepCondition
	Weight    int
	Timeout   int
}

type DependencyGraph struct {
	edges map[string][]*DependencyEdge
}

func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		edges: make(map[string][]*DependencyEdge),
	}
}

func (g *DependencyGraph) AddEdge(from, to string) {
	g.AddEdgeWithOptions(from, to, DepConditionCompleted, 1, 0)
}

func (g *DependencyGraph) AddEdgeWithOptions(from, to string, condition DepCondition, weight int, timeout int) {
	if weight <= 0 {
		weight = 1
	}
	edge := &DependencyEdge{
		From:      from,
		To:        to,
		Condition: condition,
		Weight:    weight,
		Timeout:   timeout,
	}
	g.edges[from] = append(g.edges[from], edge)
}

func (g *DependencyGraph) RemoveEdge(from, to string) bool {
	edges, ok := g.edges[from]
	if !ok {
		return false
	}
	found := false
	newEdges := make([]*DependencyEdge, 0, len(edges))
	for _, e := range edges {
		if e.To == to {
			found = true
			continue
		}
		newEdges = append(newEdges, e)
	}
	if found {
		if len(newEdges) == 0 {
			delete(g.edges, from)
		} else {
			g.edges[from] = newEdges
		}
	}
	return found
}

func (g *DependencyGraph) Dependencies(taskID string) []string {
	edges := g.edges[taskID]
	result := make([]string, len(edges))
	for i, e := range edges {
		result[i] = e.To
	}
	return result
}

func (g *DependencyGraph) DependencyEdges(taskID string) []*DependencyEdge {
	return g.edges[taskID]
}

func (g *DependencyGraph) GetEdge(from, to string) *DependencyEdge {
	edges := g.edges[from]
	for _, e := range edges {
		if e.To == to {
			return e
		}
	}
	return nil
}

func (g *DependencyGraph) Dependents(taskID string) []string {
	var result []string
	for from, edges := range g.edges {
		for _, e := range edges {
			if e.To == taskID {
				result = append(result, from)
				break
			}
		}
	}
	return result
}

func (g *DependencyGraph) AllNodes() map[string][]*DependencyEdge {
	return g.edges
}

func (g *DependencyGraph) AllEdges() []*DependencyEdge {
	var result []*DependencyEdge
	for _, edges := range g.edges {
		result = append(result, edges...)
	}
	return result
}

func (g *DependencyGraph) Copy() *DependencyGraph {
	ng := NewDependencyGraph()
	for k, edges := range g.edges {
		newEdges := make([]*DependencyEdge, len(edges))
		for i, e := range edges {
			newEdges[i] = &DependencyEdge{
				From:      e.From,
				To:        e.To,
				Condition: e.Condition,
				Weight:    e.Weight,
				Timeout:   e.Timeout,
			}
		}
		ng.edges[k] = newEdges
	}
	return ng
}

func (g *DependencyGraph) HasNode(nodeID string) bool {
	if _, ok := g.edges[nodeID]; ok {
		return true
	}
	for _, edges := range g.edges {
		for _, e := range edges {
			if e.To == nodeID {
				return true
			}
		}
	}
	return false
}

func ParseDepCondition(cond string) DepCondition {
	switch strings.ToLower(cond) {
	case "success_or_skip":
		return DepConditionSuccessOrSkip
	case "any_terminal":
		return DepConditionAnyTerminal
	case "completed", "":
		return DepConditionCompleted
	default:
		return DepConditionCompleted
	}
}

func IsDependencySatisfied(edge *DependencyEdge, getTask func(string) *model.Task) bool {
	t := getTask(edge.To)
	if t == nil {
		return false
	}
	switch edge.Condition {
	case DepConditionCompleted:
		return t.Status == model.TaskStatusCompleted
	case DepConditionSuccessOrSkip:
		return t.Status == model.TaskStatusCompleted || t.Status == model.TaskStatusSkipped
	case DepConditionAnyTerminal:
		return IsTerminalStatus(t.Status)
	default:
		return t.Status == model.TaskStatusCompleted
	}
}

func IsDependencyFailed(edge *DependencyEdge, getTask func(string) *model.Task) bool {
	t := getTask(edge.To)
	if t == nil {
		return true
	}
	switch edge.Condition {
	case DepConditionCompleted:
		return t.Status == model.TaskStatusFailed ||
			t.Status == model.TaskStatusCancelled ||
			t.Status == model.TaskStatusTimedOut ||
			t.Status == model.TaskStatusSkipped
	case DepConditionSuccessOrSkip:
		return t.Status == model.TaskStatusFailed ||
			t.Status == model.TaskStatusCancelled ||
			t.Status == model.TaskStatusTimedOut
	case DepConditionAnyTerminal:
		return false
	default:
		return t.Status == model.TaskStatusFailed ||
			t.Status == model.TaskStatusCancelled ||
			t.Status == model.TaskStatusTimedOut ||
			t.Status == model.TaskStatusSkipped
	}
}

func IsTerminalStatus(status model.TaskStatus) bool {
	return status == model.TaskStatusCompleted ||
		status == model.TaskStatusFailed ||
		status == model.TaskStatusSkipped ||
		status == model.TaskStatusCancelled ||
		status == model.TaskStatusTimedOut ||
		status == model.TaskStatusPreempted
}

func DetectCycle(g *DependencyGraph) ([]string, bool) {
	white := 0
	gray := 1
	black := 2

	allNodes := make(map[string]bool)
	for from, edges := range g.edges {
		allNodes[from] = true
		for _, e := range edges {
			allNodes[e.To] = true
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
		edges := g.edges[node]
		for _, e := range edges {
			dep := e.To
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
				edge := g.GetEdge(depID, taskID)
				if edge != nil && edge.Condition == DepConditionAnyTerminal {
					continue
				}
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
	BlockedCount       int
	AvgDependencyDepth float64
	CriticalPathLen    int
	CriticalPath       []string
}

func ComputeStats(g *DependencyGraph, getTask func(string) *model.Task) DAGStats {
	blockedCount := 0
	totalDepth := 0
	taskCount := 0

	allNodes := make(map[string]bool)
	for from, edges := range g.edges {
		allNodes[from] = true
		for _, e := range edges {
			allNodes[e.To] = true
		}
	}

	for node := range allNodes {
		t := getTask(node)
		if t != nil && t.Status == model.TaskStatusBlocked {
			blockedCount++
		}
		depth := computeWeightedDepth(g, node)
		totalDepth += depth
		taskCount++
	}

	avgDepth := 0.0
	if taskCount > 0 {
		avgDepth = float64(totalDepth) / float64(taskCount)
	}

	criticalPathLen, criticalPath := ComputeCriticalPath(g, getTask)

	return DAGStats{
		BlockedCount:       blockedCount,
		AvgDependencyDepth: avgDepth,
		CriticalPathLen:    criticalPathLen,
		CriticalPath:       criticalPath,
	}
}

func computeWeightedDepth(g *DependencyGraph, node string) int {
	edges := g.edges[node]
	if len(edges) == 0 {
		return 0
	}
	maxChildDepth := 0
	for _, e := range edges {
		d := computeWeightedDepth(g, e.To)
		weighted := d + e.Weight
		if weighted > maxChildDepth {
			maxChildDepth = weighted
		}
	}
	return maxChildDepth
}

func ComputeCriticalPath(g *DependencyGraph, getTask func(string) *model.Task) (int, []string) {
	allNodes := make(map[string]bool)
	for from, edges := range g.edges {
		allNodes[from] = true
		for _, e := range edges {
			allNodes[e.To] = true
		}
	}

	var roots []string
	dependent := make(map[string]bool)
	for _, edges := range g.edges {
		for _, e := range edges {
			dependent[e.To] = true
		}
	}
	for node := range allNodes {
		if !dependent[node] {
			roots = append(roots, node)
		}
	}

	if len(roots) == 0 {
		return 0, nil
	}

	maxWeight := 0
	var bestPath []string
	for _, root := range roots {
		w, path := dfsCriticalPath(g, root)
		if w > maxWeight {
			maxWeight = w
			bestPath = path
		}
	}

	return maxWeight, bestPath
}

func dfsCriticalPath(g *DependencyGraph, node string) (int, []string) {
	edges := g.edges[node]
	if len(edges) == 0 {
		return 0, []string{node}
	}

	maxWeight := 0
	var bestPath []string
	for _, e := range edges {
		subWeight, subPath := dfsCriticalPath(g, e.To)
		total := subWeight + e.Weight
		if total > maxWeight {
			maxWeight = total
			bestPath = append([]string{node}, subPath...)
		}
	}
	return maxWeight, bestPath
}

func RenderASCIITree(g *DependencyGraph, getTask func(string) *model.Task, rootTaskID string) string {
	allNodes := make(map[string]bool)
	for from := range g.edges {
		allNodes[from] = true
	}

	_, criticalPath := ComputeCriticalPath(g, getTask)
	criticalSet := make(map[string]bool)
	for _, n := range criticalPath {
		criticalSet[n] = true
	}

	var roots []string
	if rootTaskID != "" {
		roots = []string{rootTaskID}
	} else {
		dependent := make(map[string]bool)
		for _, edges := range g.edges {
			for _, e := range edges {
				dependent[e.To] = true
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
		renderNode(&sb, g, getTask, root, "", true, visited, criticalSet)
	}

	return sb.String()
}

func renderNode(sb *strings.Builder, g *DependencyGraph, getTask func(string) *model.Task, nodeID, prefix string, isLast bool, visited map[string]bool, criticalSet map[string]bool) {
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

	prefixMark := ""
	if criticalSet[nodeID] {
		prefixMark = "*"
	}

	sb.WriteString(prefix + selectedConnector + prefixMark + name + statusStr + "\n")

	if visited[nodeID] {
		sb.WriteString(prefix + childPrefix(isLast, len(prefix) == 0) + "  (see above)\n")
		return
	}
	visited[nodeID] = true

	edges := g.edges[nodeID]
	for i, e := range edges {
		dep := e.To
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
		isDepLast := i == len(edges)-1
		renderNode(sb, g, getTask, dep, childPrefix, isDepLast, visited, criticalSet)
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

	edges := g.edges[rootTaskID]
	if len(edges) > 0 {
		prefix := "└── depends on:\n"
		if len(dependents) == 0 {
			prefix = "├── depends on:\n"
		}
		sb.WriteString(prefix)
		for i, e := range edges {
			depID := e.To
			childPrefix := "    ├── "
			if i == len(edges)-1 {
				childPrefix = "    └── "
			}
			dt := getTask(depID)
			depName := depID
			depStatus := ""
			if dt != nil {
				depName = fmt.Sprintf("%s (%s)", dt.Spec.Name, depID)
				depStatus = fmt.Sprintf(" [%s]", string(dt.Status))
			}
			edgeInfo := ""
			if e.Condition != DepConditionCompleted || e.Weight != 1 || e.Timeout > 0 {
				var parts []string
				if e.Condition != DepConditionCompleted {
					parts = append(parts, string(e.Condition))
				}
				if e.Weight != 1 {
					parts = append(parts, fmt.Sprintf("weight=%d", e.Weight))
				}
				if e.Timeout > 0 {
					parts = append(parts, fmt.Sprintf("timeout=%dm", e.Timeout))
				}
				edgeInfo = " (" + strings.Join(parts, ", ") + ")"
			}
			sb.WriteString(childPrefix + depName + depStatus + edgeInfo + "\n")

			subEdges := g.edges[depID]
			for j, se := range subEdges {
				subDepID := se.To
				subPrefix := "        ├── "
				if j == len(subEdges)-1 {
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
	for from, edges := range g.edges {
		allNodes[from] = true
		for _, e := range edges {
			allNodes[e.To] = true
		}
	}

	_, criticalPath := ComputeCriticalPath(g, getTask)
	criticalSet := make(map[string]bool)
	criticalEdgeSet := make(map[string]bool)
	for _, n := range criticalPath {
		criticalSet[n] = true
	}
	for i := 0; i < len(criticalPath)-1; i++ {
		key := criticalPath[i] + "->" + criticalPath[i+1]
		criticalEdgeSet[key] = true
	}

	for node := range allNodes {
		t := getTask(node)
		label := node
		color := "white"
		if t != nil {
			label = fmt.Sprintf("%s\\n(%s)", t.Spec.Name, node)
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
		if criticalSet[node] {
			sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", fillcolor=%s, style=\"rounded,filled,bold\"];\n", node, label, color))
		} else {
			sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", fillcolor=%s, style=\"rounded,filled\"];\n", node, label, color))
		}
	}

	sb.WriteString("\n")

	for from, edges := range g.edges {
		for _, e := range edges {
			edgeLabel := ""
			style := ""
			key := from + "->" + e.To
			if criticalEdgeSet[key] {
				style = " [style=bold, color=red]"
			}
			if e.Weight != 1 || e.Condition != DepConditionCompleted || e.Timeout > 0 {
				var parts []string
				if e.Weight != 1 {
					parts = append(parts, fmt.Sprintf("w=%d", e.Weight))
				}
				if e.Condition != DepConditionCompleted {
					parts = append(parts, string(e.Condition))
				}
				if e.Timeout > 0 {
					parts = append(parts, fmt.Sprintf("t=%dm", e.Timeout))
				}
				edgeLabel = fmt.Sprintf(" [label=\"%s\"]", strings.Join(parts, ","))
			}
			sb.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\"%s%s;\n", from, e.To, style, edgeLabel))
		}
	}

	sb.WriteString("}\n")
	return sb.String()
}

type DepTimeoutTracker struct {
	StartTime   map[string]time.Time
	TimedOutSet map[string]map[string]bool
}

func NewDepTimeoutTracker() *DepTimeoutTracker {
	return &DepTimeoutTracker{
		StartTime:   make(map[string]time.Time),
		TimedOutSet: make(map[string]map[string]bool),
	}
}

func (t *DepTimeoutTracker) MarkTimedOut(taskID, depID string) {
	if _, ok := t.TimedOutSet[taskID]; !ok {
		t.TimedOutSet[taskID] = make(map[string]bool)
	}
	t.TimedOutSet[taskID][depID] = true
}

func (t *DepTimeoutTracker) IsTimedOut(taskID, depID string) bool {
	if set, ok := t.TimedOutSet[taskID]; ok {
		return set[depID]
	}
	return false
}

func (t *DepTimeoutTracker) Reset(taskID string) {
	delete(t.StartTime, taskID)
	delete(t.TimedOutSet, taskID)
}
