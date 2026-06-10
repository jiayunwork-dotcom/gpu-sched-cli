package model

type GPUModel string

const (
	GPUModelA100 GPUModel = "A100"
	GPUModelH100 GPUModel = "H100"
	GPUModelV100 GPUModel = "V100"
	GPUModelA800 GPUModel = "A800"
	GPUModelH800 GPUModel = "H800"
	GPUModelA30  GPUModel = "A30"
	GPUModelL40  GPUModel = "L40"
)

type GPUStatus string

const (
	GPUStatusFree     GPUStatus = "free"
	GPUStatusAllocated GPUStatus = "allocated"
	GPUStatusShared   GPUStatus = "shared"
	GPUStatusFault    GPUStatus = "fault"
)

type GPU struct {
	ID           string    `yaml:"id" json:"id"`
	Model        GPUModel  `yaml:"model" json:"model"`
	MemoryGB     int       `yaml:"memory_gb" json:"memory_gb"`
	ComputeTFLOPS float64  `yaml:"compute_tflops" json:"compute_tflops"`
	Status       GPUStatus `yaml:"-" json:"status"`
	AllocatedMemory int    `yaml:"-" json:"allocated_memory"`
	NodeName     string    `yaml:"-" json:"node_name"`
	TaskIDs      []string  `yaml:"-" json:"task_ids"`
}

func (g *GPU) AvailableMemory() int {
	return g.MemoryGB - g.AllocatedMemory
}

func (g *GPU) CanAllocate(needMemory int) bool {
	if g.Status == GPUStatusFault {
		return false
	}
	if g.Status != GPUStatusFree {
		return false
	}
	return g.AvailableMemory() >= needMemory
}

func (g *GPU) CanShare(needMemory int) bool {
	if g.Status == GPUStatusFault {
		return false
	}
	if g.Status != GPUStatusShared && g.Status != GPUStatusAllocated {
		return false
	}
	totalUsed := g.AllocatedMemory + needMemory
	if totalUsed > int(float64(g.MemoryGB)*0.9) {
		return false
	}
	return g.AvailableMemory() >= needMemory
}

type NVLink struct {
	GPU1ID   string  `yaml:"gpu1" json:"gpu1"`
	GPU2ID   string  `yaml:"gpu2" json:"gpu2"`
	Bandwidth float64 `yaml:"bandwidth_gbps" json:"bandwidth_gbps"`
}

type Node struct {
	Name         string    `yaml:"name" json:"name"`
	GPUs         []*GPU    `yaml:"gpus" json:"gpus"`
	CPUcores     int       `yaml:"cpu_cores" json:"cpu_cores"`
	MemoryGB     int       `yaml:"memory_gb" json:"memory_gb"`
	NetworkGbps  float64   `yaml:"network_gbps" json:"network_gbps"`
	NVLinks      []NVLink  `yaml:"nvlinks" json:"nvlinks"`
	Status       string    `yaml:"-" json:"status"`
	UsedCPU      int       `yaml:"-" json:"used_cpu"`
	UsedMemory   int       `yaml:"-" json:"used_memory"`
}

func (n *Node) AvailableCPU() int {
	return n.CPUcores - n.UsedCPU
}

func (n *Node) AvailableMemory() int {
	return n.MemoryGB - n.UsedMemory
}

func (n *Node) FreeGPUCount() int {
	count := 0
	for _, g := range n.GPUs {
		if g.Status == GPUStatusFree {
			count++
		}
	}
	return count
}

func (n *Node) FreeGPUs() []*GPU {
	var result []*GPU
	for _, g := range n.GPUs {
		if g.Status == GPUStatusFree {
			result = append(result, g)
		}
	}
	return result
}

func (n *Node) SharedGPUs() []*GPU {
	var result []*GPU
	for _, g := range n.GPUs {
		if g.Status == GPUStatusShared {
			result = append(result, g)
		}
	}
	return result
}

func (n *Node) GPUUtilization() float64 {
	if len(n.GPUs) == 0 {
		return 0
	}
	used := 0
	for _, g := range n.GPUs {
		if g.Status == GPUStatusAllocated || g.Status == GPUStatusShared {
			used++
		}
	}
	return float64(used) / float64(len(n.GPUs)) * 100
}

type ClusterConfig struct {
	Nodes []*Node `yaml:"nodes"`
}

type Cluster struct {
	Nodes map[string]*Node
}

func NewCluster(config *ClusterConfig) *Cluster {
	c := &Cluster{
		Nodes: make(map[string]*Node),
	}
	for _, node := range config.Nodes {
		node.Status = "online"
		for _, gpu := range node.GPUs {
			gpu.Status = GPUStatusFree
			gpu.NodeName = node.Name
			gpu.TaskIDs = []string{}
		}
		c.Nodes[node.Name] = node
	}
	return c
}

func (c *Cluster) TotalGPUs() int {
	total := 0
	for _, n := range c.Nodes {
		if n.Status == "online" {
			total += len(n.GPUs)
		}
	}
	return total
}

func (c *Cluster) UsedGPUs() int {
	used := 0
	for _, n := range c.Nodes {
		if n.Status == "online" {
			for _, g := range n.GPUs {
				if g.Status == GPUStatusAllocated || g.Status == GPUStatusShared {
					used++
				}
			}
		}
	}
	return used
}

func (c *Cluster) GPUUtilization() float64 {
	total := c.TotalGPUs()
	if total == 0 {
		return 0
	}
	return float64(c.UsedGPUs()) / float64(total) * 100
}

func (c *Cluster) TotalMemory() int {
	total := 0
	for _, n := range c.Nodes {
		if n.Status == "online" {
			total += n.MemoryGB
		}
	}
	return total
}

func (c *Cluster) UsedMemory() int {
	used := 0
	for _, n := range c.Nodes {
		if n.Status == "online" {
			used += n.UsedMemory
		}
	}
	return used
}

func (c *Cluster) AllFreeGPUs() []*GPU {
	var gpus []*GPU
	for _, n := range c.Nodes {
		if n.Status == "online" {
			gpus = append(gpus, n.FreeGPUs()...)
		}
	}
	return gpus
}

func (c *Cluster) FindGPUByID(id string) *GPU {
	for _, n := range c.Nodes {
		for _, g := range n.GPUs {
			if g.ID == id {
				return g
			}
		}
	}
	return nil
}

func (c *Cluster) FindNodeByGPUID(id string) *Node {
	for _, n := range c.Nodes {
		for _, g := range n.GPUs {
			if g.ID == id {
				return n
			}
		}
	}
	return nil
}

func (c *Cluster) GetNVLinkDomain(nodeName string) map[string][]string {
	node, ok := c.Nodes[nodeName]
	if !ok {
		return nil
	}
	domain := make(map[string][]string)
	for _, link := range node.NVLinks {
		domain[link.GPU1ID] = append(domain[link.GPU1ID], link.GPU2ID)
		domain[link.GPU2ID] = append(domain[link.GPU2ID], link.GPU1ID)
	}
	return domain
}

func (c *Cluster) AreNVLinked(gpu1ID, gpu2ID string) bool {
	for _, n := range c.Nodes {
		for _, link := range n.NVLinks {
			if (link.GPU1ID == gpu1ID && link.GPU2ID == gpu2ID) ||
				(link.GPU1ID == gpu2ID && link.GPU2ID == gpu1ID) {
				return true
			}
		}
	}
	return false
}

func (c *Cluster) SetNodeStatus(name string, status string) {
	node, ok := c.Nodes[name]
	if !ok {
		return
	}
	node.Status = status
	if status == "offline" {
		for _, gpu := range node.GPUs {
			gpu.Status = GPUStatusFault
		}
	}
}
