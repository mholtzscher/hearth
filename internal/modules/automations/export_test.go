package automations

import "context"

// WaitForRunWorkers lets behavior tests join workers without shutting down the service.
func WaitForRunWorkers(ctx context.Context, service *Service) error {
	return service.admission.Wait(ctx)
}
