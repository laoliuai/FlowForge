package dag

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowforge/flowforge/pkg/model"
)

type DAGSpec struct {
	Version    string               `json:"version"`
	Entrypoint string               `json:"entrypoint"`
	Templates  map[string]*Template `json:"templates"`
	OnExit     string               `json:"onExit,omitempty"`
}

type Template struct {
	DAG       *DAGTemplate       `json:"dag,omitempty"`
	Container *ContainerTemplate `json:"container,omitempty"`
	Inputs    *Inputs            `json:"inputs,omitempty"`
	Outputs   *Outputs           `json:"outputs,omitempty"`
	Retry     *RetryStrategy     `json:"retryStrategy,omitempty"`
}

type DAGTemplate struct {
	Tasks []DAGTask `json:"tasks"`
}

type DAGTask struct {
	Name         string     `json:"name"`
	Template     string     `json:"template"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Arguments    *Arguments `json:"arguments,omitempty"`
	When         string     `json:"when,omitempty"`
	Replicas     int        `json:"replicas,omitempty"`
}

type ContainerTemplate struct {
	Image     string        `json:"image"`
	Command   []string      `json:"command,omitempty"`
	Args      []string      `json:"args,omitempty"`
	Env       []EnvVar      `json:"env,omitempty"`
	Resources *ResourceSpec `json:"resources,omitempty"`
}

type ResourceSpec struct {
	Requests ResourceRequirements `json:"requests,omitempty"`
	Limits   ResourceRequirements `json:"limits,omitempty"`
}

type ResourceRequirements struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	GPU    string `json:"nvidia.com/gpu,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Arguments struct {
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Inputs struct {
	Parameters []InputParameter `json:"parameters,omitempty"`
}

type InputParameter struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}

type Outputs struct {
	Parameters []OutputParameter `json:"parameters,omitempty"`
}

type OutputParameter struct {
	Name      string    `json:"name"`
	ValueFrom ValueFrom `json:"valueFrom"`
}

type ValueFrom struct {
	Path string `json:"path"`
}

type RetryStrategy struct {
	Limit   int            `json:"limit"`
	Backoff *BackoffPolicy `json:"backoff,omitempty"`
}

type BackoffPolicy struct {
	Duration    string  `json:"duration"`
	Factor      float64 `json:"factor"`
	MaxDuration string  `json:"maxDuration"`
}

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Parse(workflowID string, spec model.JSONB) ([]*model.Task, error) {
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spec: %w", err)
	}

	var dagSpec DAGSpec
	if err := json.Unmarshal(specBytes, &dagSpec); err != nil {
		return nil, fmt.Errorf("failed to parse DAG spec: %w", err)
	}

	if err := p.validate(&dagSpec); err != nil {
		return nil, err
	}

	entryTemplate, ok := dagSpec.Templates[dagSpec.Entrypoint]
	if !ok {
		return nil, fmt.Errorf("entrypoint template '%s' not found", dagSpec.Entrypoint)
	}

	if entryTemplate.DAG == nil {
		return nil, fmt.Errorf("entrypoint template must be a DAG template")
	}

	tasks := make([]*model.Task, 0, len(entryTemplate.DAG.Tasks))
	taskGroups := make(map[string][]*model.Task)

	for _, dagTask := range entryTemplate.DAG.Tasks {
		replicas := dagTask.Replicas
		if replicas <= 0 {
			replicas = 1
		}
		for replicaIndex := 0; replicaIndex < replicas; replicaIndex++ {
			task, err := p.convertTask(workflowID, &dagTask, &dagSpec)
			if err != nil {
				return nil, fmt.Errorf("failed to convert task '%s': %w", dagTask.Name, err)
			}
			task.GangID = dagTask.Name
			task.ReplicaIndex = replicaIndex
			if replicas > 1 {
				task.Name = fmt.Sprintf("%s-%d", dagTask.Name, replicaIndex)
			}
			tasks = append(tasks, task)
			taskGroups[dagTask.Name] = append(taskGroups[dagTask.Name], task)
		}
	}

	for _, dagTask := range entryTemplate.DAG.Tasks {
		depTasks, ok := taskGroups[dagTask.Name]
		if !ok {
			continue
		}
		for _, task := range depTasks {
			for _, depName := range dagTask.Dependencies {
				dependencyGroup, ok := taskGroups[depName]
				if !ok {
					return nil, fmt.Errorf("dependency '%s' not found for task '%s'", depName, dagTask.Name)
				}
				for _, dependencyTask := range dependencyGroup {
					task.Dependencies = append(task.Dependencies, model.TaskDependency{
						TaskID:      task.ID,
						DependsOnID: dependencyTask.ID,
						Type:        "success",
					})
				}
			}
		}
	}

	return tasks, nil
}

func (p *Parser) convertTask(workflowID string, dagTask *DAGTask, spec *DAGSpec) (*model.Task, error) {
	template, ok := spec.Templates[dagTask.Template]
	if !ok {
		return nil, fmt.Errorf("template '%s' not found", dagTask.Template)
	}

	if template.Container == nil {
		return nil, fmt.Errorf("template '%s' must have a container definition", dagTask.Template)
	}

	task := &model.Task{
		ID:            uuid.New(),
		WorkflowID:    uuid.MustParse(workflowID),
		Name:          dagTask.Name,
		TemplateName:  dagTask.Template,
		Image:         template.Container.Image,
		Command:       template.Container.Command,
		Args:          p.substituteArgs(template.Container.Args, dagTask.Arguments),
		WhenCondition: dagTask.When,
		Status:        model.TaskPending,
	}

	if template.Container.Resources != nil {
		requests := template.Container.Resources.Requests
		limits := template.Container.Resources.Limits
		if requests.CPU != "" {
			task.CPURequest = parseCPU(requests.CPU)
		}
		if limits.CPU != "" {
			task.CPULimit = parseCPU(limits.CPU)
		}
		if requests.Memory != "" {
			task.MemoryRequest = parseMemory(requests.Memory)
		}
		if limits.Memory != "" {
			task.MemoryLimit = parseMemory(limits.Memory)
		}
		if requests.GPU != "" {
			task.GPURequest = parseGPU(requests.GPU)
		}
	}

	if template.Retry != nil {
		task.RetryLimit = template.Retry.Limit
		if template.Retry.Backoff != nil && template.Retry.Backoff.Duration != "" {
			if duration, err := time.ParseDuration(template.Retry.Backoff.Duration); err == nil {
				task.BackoffSecs = int(duration.Seconds())
			}
		}
	}

	if len(template.Container.Env) > 0 {
		envMap := make(map[string]interface{}, len(template.Container.Env))
		for _, env := range template.Container.Env {
			envMap[env.Name] = env.Value
		}
		task.Env = model.JSONB(envMap)
	}

	return task, nil
}

func (p *Parser) substituteArgs(args []string, arguments *Arguments) []string {
	if arguments == nil {
		return args
	}

	paramMap := make(map[string]string)
	for _, param := range arguments.Parameters {
		paramMap[param.Name] = param.Value
	}

	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = substituteParameters(arg, paramMap)
	}
	return result
}

func (p *Parser) validate(spec *DAGSpec) error {
	if spec.Entrypoint == "" {
		return fmt.Errorf("entrypoint is required")
	}
	if len(spec.Templates) == 0 {
		return fmt.Errorf("at least one template is required")
	}
	return p.detectCycles(spec)
}

func (p *Parser) detectCycles(spec *DAGSpec) error {
	template, ok := spec.Templates[spec.Entrypoint]
	if !ok || template.DAG == nil {
		return nil
	}

	graph := make(map[string][]string)
	for _, task := range template.DAG.Tasks {
		graph[task.Name] = task.Dependencies
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(node string) bool
	hasCycle = func(node string) bool {
		visited[node] = true
		recStack[node] = true

		for _, dep := range graph[node] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if recStack[dep] {
				return true
			}
		}

		recStack[node] = false
		return false
	}

	for node := range graph {
		if !visited[node] {
			if hasCycle(node) {
				return fmt.Errorf("cycle detected in DAG")
			}
		}
	}

	return nil
}

func parseCPU(cpu string) int {
	if strings.HasSuffix(cpu, "m") {
		v, _ := strconv.Atoi(strings.TrimSuffix(cpu, "m"))
		return v
	}
	v, _ := strconv.ParseFloat(cpu, 64)
	return int(v * 1000)
}

func parseMemory(mem string) int {
	mem = strings.TrimSpace(mem)
	multiplier := 1
	unit := ""

	switch {
	case strings.HasSuffix(mem, "Gi"):
		multiplier = 1024
		unit = "Gi"
		mem = strings.TrimSuffix(mem, "Gi")
	case strings.HasSuffix(mem, "Mi"):
		multiplier = 1
		unit = "Mi"
		mem = strings.TrimSuffix(mem, "Mi")
	case strings.HasSuffix(mem, "Ki"):
		multiplier = 1
		unit = "Ki"
		mem = strings.TrimSuffix(mem, "Ki")
	}

	v, _ := strconv.Atoi(mem)
	if unit == "Ki" {
		return v / 1024
	}
	return v * multiplier
}

func parseGPU(gpu string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(gpu))
	return value
}

func substituteParameters(s string, params map[string]string) string {
	for k, v := range params {
		s = strings.ReplaceAll(s, "{{inputs.parameters."+k+"}}", v)
	}
	return s
}
