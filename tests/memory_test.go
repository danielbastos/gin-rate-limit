package tests

import (
	"testing"

	"github.com/danielbastos/gin-rate-limit/storage"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
	"github.com/danielbastos/gin-rate-limit/tests/contract"
)

func TestMemory(t *testing.T) {
	events := func(*testing.T) storage.EventStore { return memory.NewEventStore() }
	buckets := func(*testing.T) storage.BucketStore { return memory.NewBucketStore() }
	t.Run("events", func(t *testing.T) { contract.RunEventStore(t, events) })
	t.Run("buckets", func(t *testing.T) { contract.RunBucketStore(t, buckets) })
	t.Run("limiters", func(t *testing.T) { contract.RunLimiters(t, events, buckets) })
}
