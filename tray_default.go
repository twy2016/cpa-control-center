//go:build !windows

package main

type noopTrayController struct{}

func newTrayController(labels trayLabels, actions trayActions) (trayController, error) {
	return &noopTrayController{}, nil
}

func (n *noopTrayController) Ready() bool {
	return false
}

func (n *noopTrayController) UpdateLabels(labels trayLabels) {}

func (n *noopTrayController) Close() error {
	return nil
}
