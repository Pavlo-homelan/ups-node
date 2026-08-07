package runtime

import (
	"context"
	"fmt"

	"ups-pi-node/internal/helper"
)

func Router(controller *Controller) helper.Router {
	return helper.Router{
		"ups.status": func(context.Context, string, map[string]any) (any, error) {
			if controller == nil {
				return nil, fmt.Errorf("UPS hardware controller is not enabled")
			}
			return controller.Status(), nil
		},
	}
}
