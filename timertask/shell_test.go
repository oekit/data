package timertask_test

import (
	"context"
	"testing"

	"github.com/oekit/data/internal/testsame"
	"github.com/oekit/data/timertask"
	timertasksrc "github.com/oekit/timertask"
)

// TestTypeIdentity Handler/Task 类型双向赋值。
func TestTypeIdentity(t *testing.T) {
	h := timertask.Handler(func(ctx context.Context, task *timertask.Task) error {
		return nil
	})
	testsame.Assert[timertasksrc.Handler](h)

	task := timertask.Task{ID: "shell"}
	testsame.Assert[*timertasksrc.Task](&task)
}
