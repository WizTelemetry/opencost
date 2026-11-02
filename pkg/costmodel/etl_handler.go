package costmodel

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// GetETLStatus reports the state of the lightweight ETL worker.
// @Summary      查询 ETL 状态
// @Tags         ETL
// @Description  返回 ETL 开关、存储路径、最近运行时间和桶构建结果。
// @Success      200  {object}  costmodel.ETLWorkerStatus
// @Router       /etl/status [get]
// @Router       /kapis/costwise.wiztelemetry.io/v1alpha1/etl/status [get]
func (a *Accesses) GetETLStatus(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "application/json")

	status := ETLWorkerStatus{Enabled: false}
	if a != nil && a.ETLWorker != nil {
		status = a.ETLWorker.Status()
	}
	proto.WriteData(w, status)
}
