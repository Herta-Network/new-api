package payment

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(string) error {
		common.IsMasterNode = true
		common.RedisEnabled = false
		common.BatchUpdateEnabled = false
		common.LogConsumeEnabled = true
		if err := model.InitDB(); err != nil {
			return err
		}
		if err := model.InitLogDB(); err != nil {
			return err
		}
		return Init()
	}))
}
