package webapp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type dashboardWidgetDefinition struct {
	ID       string
	Kind     string
	LabelKey string
}

type dashboardWidget struct {
	ID       string
	Kind     string
	LabelKey string
}

type dashboardWidgetManager struct {
	layoutPath string
}

var dashboardWidgetDefinitions = map[string]dashboardWidgetDefinition{
	"ups-main":     {ID: "ups-main", Kind: "ups", LabelKey: "dashboard.widget.ups"},
	"ups-overview": {ID: "ups-overview", Kind: "ups_overview", LabelKey: "dashboard.widget.ups_overview"},
	"cpu-temp":     {ID: "cpu-temp", Kind: "sensor_cpu", LabelKey: "dashboard.widget.cpu"},
	"ram-usage":    {ID: "ram-usage", Kind: "sensor_ram", LabelKey: "dashboard.widget.ram"},
	"wifi-status":  {ID: "wifi-status", Kind: "status_wifi", LabelKey: "dashboard.widget.wifi"},
}

var dashboardWidgetOrder = []string{"ups-main", "ups-overview", "cpu-temp", "ram-usage", "wifi-status"}

func newDashboardWidgetManager(layoutPath string) dashboardWidgetManager {
	return dashboardWidgetManager{layoutPath: layoutPath}
}

func (manager dashboardWidgetManager) listActive() []dashboardWidget {
	return normalizeDashboardWidgets(manager.readLayout())
}

func (manager dashboardWidgetManager) listAvailable() []dashboardWidget {
	active := map[string]struct{}{}
	for _, widget := range manager.listActive() {
		active[widget.ID] = struct{}{}
	}
	var available []dashboardWidget
	for _, id := range dashboardWidgetOrder {
		if _, ok := active[id]; ok {
			continue
		}
		definition := dashboardWidgetDefinitions[id]
		available = append(available, dashboardWidget(definition))
	}
	return available
}

func (manager dashboardWidgetManager) add(widgetID string) (bool, error) {
	definition, ok := dashboardWidgetDefinitions[widgetID]
	if !ok {
		return false, nil
	}
	widgets := manager.listActive()
	for _, widget := range widgets {
		if widget.ID == widgetID {
			return true, nil
		}
	}
	widgets = append(widgets, dashboardWidget(definition))
	if err := manager.writeLayout(widgets); err != nil {
		return false, err
	}
	return true, nil
}

func (manager dashboardWidgetManager) remove(widgetID string) (bool, error) {
	widgets := manager.listActive()
	next := make([]dashboardWidget, 0, len(widgets))
	for _, widget := range widgets {
		if widget.ID != widgetID {
			next = append(next, widget)
		}
	}
	if len(next) == len(widgets) {
		return false, nil
	}
	if err := manager.writeLayout(next); err != nil {
		return false, err
	}
	return true, nil
}

func (manager dashboardWidgetManager) readLayout() []map[string]string {
	contents, err := os.ReadFile(manager.layoutPath)
	if err != nil {
		return []map[string]string{{"id": "ups-main", "kind": "ups"}}
	}
	var object struct {
		Widgets []map[string]string `json:"widgets"`
	}
	if err := json.Unmarshal(contents, &object); err == nil && object.Widgets != nil {
		return object.Widgets
	}
	var array []map[string]string
	if err := json.Unmarshal(contents, &array); err == nil && array != nil {
		return array
	}
	return []map[string]string{{"id": "ups-main", "kind": "ups"}}
}

func (manager dashboardWidgetManager) writeLayout(widgets []dashboardWidget) error {
	if err := os.MkdirAll(filepath.Dir(manager.layoutPath), 0o770); err != nil {
		return err
	}
	payload := struct {
		Widgets []map[string]string `json:"widgets"`
	}{Widgets: make([]map[string]string, 0, len(widgets))}
	for _, widget := range widgets {
		payload.Widgets = append(payload.Widgets, map[string]string{"id": widget.ID, "kind": widget.Kind})
	}
	contents, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return os.WriteFile(manager.layoutPath, contents, 0o660)
}

func normalizeDashboardWidgets(widgets []map[string]string) []dashboardWidget {
	seen := map[string]struct{}{}
	var normalized []dashboardWidget
	for _, item := range widgets {
		widgetID := item["id"]
		definition, ok := dashboardWidgetDefinitions[widgetID]
		if !ok {
			continue
		}
		if _, ok := seen[widgetID]; ok {
			continue
		}
		seen[widgetID] = struct{}{}
		normalized = append(normalized, dashboardWidget(definition))
	}
	return normalized
}
