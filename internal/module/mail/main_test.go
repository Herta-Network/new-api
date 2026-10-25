package mail

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(string) error {
		common.IsMasterNode = true
		common.RedisEnabled = false
		if err := i18n.Init(); err != nil {
			return err
		}
		return model.InitDB()
	}))
}
