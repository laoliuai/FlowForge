package dag

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/flowforge/flowforge/pkg/model"
)

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Parse(workflowID string, spec model.JSONB) ([]*model.Task, error) {
	entrypoint, ok := spec["entrypoint"].(string)
	if !ok || entrypoint == "" {
		return nil, errors.New("missing entrypoint")
	}

	templates, ok := spec["templates"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing templates")
	}

	entryTemplate, ok := templates[entrypoint].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("entrypoint template %s not found", entrypoint)
	}

	dagSpec, ok := entryTemplate["dag"].(map[string]interface{})
	if !ok {
		return nil, errors.New("entrypoint missing dag")
	}

	tasksSpec, ok := dagSpec["tasks"].([]interface{})
	if !ok {
		return nil, errors.New("dag tasks missing")
	}

	workflowUUID, err := uuid.Parse(workflowID)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow id: %w", err)
	}

	tasks := make([]*model.Task, 0, len(tasksSpec))
	taskByName := make(map[string]*model.Task)
	dependencyMap := make(map[string][]string)

	for _, taskItem := range tasksSpec {
		item, ok := taskItem.(map[string]interface{})
		if !ok {
			return nil, errors.New("invalid task spec")
		}
		name, _ := item["name"].(string)
		templateName, _ := item["template"].(string)
		if name == "" || templateName == "" {
			return nil, errors.New("task missing name or template")
		}

		template, ok := templates[templateName].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("template %s not found", templateName)
		}

		container, ok := template["container"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("template %s missing container", templateName)
		}

		image, _ := container["image"].(string)
		command := parseStringSlice(container["command"])
		args := parseStringSlice(container["args"])
		whenCondition, _ := item["when"].(string)
		dependencies := parseStringSlice(item["dependencies"])

		task := &model.Task{
			ID:            uuid.New(),
			WorkflowID:    workflowUUID,
			Name:          name,
			Image:         image,
			Command:       command,
			Args:          args,
			Status:        model.TaskPending,
			TemplateName:  templateName,
			WhenCondition: whenCondition,
		}

		tasks = append(tasks, task)
		taskByName[name] = task
		if len(dependencies) > 0 {
			dependencyMap[name] = dependencies
		}
	}

	for taskName, deps := range dependencyMap {
		task, ok := taskByName[taskName]
		if !ok {
			return nil, fmt.Errorf("task %s not found for dependencies", taskName)
		}
		for _, depName := range deps {
			depTask, ok := taskByName[depName]
			if !ok {
				return nil, fmt.Errorf("dependency %s not found for task %s", depName, taskName)
			}
			task.Dependencies = append(task.Dependencies, model.TaskDependency{
				TaskID:      task.ID,
				DependsOnID: depTask.ID,
				Type:        "success",
			})
		}
	}

	return tasks, nil
}

func parseStringSlice(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		results := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := item.(string); ok {
				results = append(results, str)
			}
		}
		return results
	default:
		return nil
	}
}
