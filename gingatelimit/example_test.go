package gingatelimit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/gingatelimit"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
	"github.com/gin-gonic/gin"
)

func ExampleNew() {
	gin.SetMode(gin.ReleaseMode)
	l, err := ratelimit.NewEventLog(memory.NewEventStore())
	if err != nil {
		panic(err)
	}
	m, err := gingatelimit.New(l, gingatelimit.Config{
		Limit: 1, Window: time.Minute,
		KeyFunc: func(c *gin.Context) (ratelimit.Key, error) {
			return ratelimit.NewKey("ping", "ip", c.ClientIP())
		},
	})
	if err != nil {
		panic(err)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	r.GET("/ping", m, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
		fmt.Println(w.Code)
	}
	// Output:
	// 204
	// 429
}
