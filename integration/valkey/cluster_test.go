package valkey

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/valkey-io/valkey-go"
)

// clusterPorts are the ports the three nodes of the test cluster listen on inside
// their container. Each node's cluster bus is on its port plus 10000.
var clusterPorts = []string{"7000", "7001", "7002"}

// startValkeyCluster brings up a Valkey cluster of three primaries, each owning a
// third of the hash slots, and returns the address of the first. Three shards are
// what make a keyless command reach only part of the key space, and a command
// whose keys span hash slots span nodes.
//
// The nodes run in one container, so that they reach each other's cluster bus on
// the loopback interface. Each announces 127.0.0.1 and the port the test reaches
// it on, rather than its port inside the container, because a cluster client
// sends every command to the address the cluster announces for its slot.
//
// Call it before servicetest.New, for the reason given on startValkey.
func startValkeyCluster(tb testing.TB) string {
	tb.Helper()

	// The shell is the container's init: it has to act on the SIGTERM that stops
	// the container, which it would otherwise ignore until Docker kills it.
	var script strings.Builder
	script.WriteString("trap 'exit 0' TERM; ")
	exposed := make([]string, 0, len(clusterPorts))
	strategies := make([]wait.Strategy, 0, len(clusterPorts))
	for _, port := range clusterPorts {
		script.WriteString("valkey-server --port " + port + " --cluster-enabled yes --cluster-config-file nodes-" + port + ".conf --save '' --appendonly no & ")
		exposed = append(exposed, port+"/tcp")
		strategies = append(strategies, wait.ForListeningPort(port+"/tcp"))
	}
	script.WriteString("wait")

	ctx := tb.Context()
	container, err := testcontainers.Run(ctx, "valkey/valkey:8-alpine",
		testcontainers.WithEntrypoint("sh", "-c", script.String()),
		testcontainers.WithExposedPorts(exposed...),
		testcontainers.WithWaitStrategy(strategies...),
	)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	cli := func(port string, args ...string) string {
		code, out, err := container.Exec(ctx, append([]string{"valkey-cli", "-p", port}, args...), tcexec.Multiplexed())
		require.NoError(tb, err)

		body, err := io.ReadAll(out)
		require.NoError(tb, err)
		require.Zero(tb, code, string(body))

		return string(body)
	}

	mapped := make([]string, len(clusterPorts))
	for i, port := range clusterPorts {
		p, err := container.MappedPort(ctx, port+"/tcp")
		require.NoError(tb, err)
		mapped[i] = p.Port()

		bus, err := strconv.Atoi(port)
		require.NoError(tb, err)

		cli(port, "config", "set", "cluster-announce-ip", "127.0.0.1")
		cli(port, "config", "set", "cluster-announce-port", mapped[i])
		cli(port, "config", "set", "cluster-announce-bus-port", strconv.Itoa(bus+10000))
	}

	for i, port := range clusterPorts {
		first, last := shardSlots(i)
		cli(port, "cluster", "addslotsrange", strconv.Itoa(first), strconv.Itoa(last))

		if i > 0 {
			bus, err := strconv.Atoi(port)
			require.NoError(tb, err)

			cli(clusterPorts[0], "cluster", "meet", "127.0.0.1", port, strconv.Itoa(bus+10000))
		}
	}

	require.Eventually(tb, func() bool {
		for _, port := range clusterPorts {
			info := cli(port, "cluster", "info")
			if !strings.Contains(info, "cluster_state:ok") || !strings.Contains(info, "cluster_known_nodes:3") {
				return false
			}
		}

		return true
	}, 30*time.Second, 100*time.Millisecond)

	return net.JoinHostPort("127.0.0.1", mapped[0])
}

// shardSlots returns the first and last hash slot of the i-th node of the test
// cluster.
func shardSlots(i int) (int, int) {
	const slots = 16384

	return i * slots / len(clusterPorts), (i+1)*slots/len(clusterPorts) - 1
}

// shardOf returns the node of the test cluster owning slot.
func shardOf(slot int64) int {
	for i := range clusterPorts {
		if _, last := shardSlots(i); slot <= int64(last) {
			return i
		}
	}

	return -1
}

// TestValkey_Cluster pins the commands spanning several keys, or none, against a
// cluster.
//
// MGet and Delete used to build one command over every key, which the cluster
// command builder answers with a panic as soon as two keys fall in different
// hash slots. Scan used to send its keyless SCAN to whichever node the client
// picked, and returned that one shard's keys.
func TestValkey_Cluster(t *testing.T) {
	address := startValkeyCluster(t)
	svc, _ := servicetest.New(t)

	v, err := Connect(t.Context(), svc, Config{Address: address})
	require.NoError(t, err)

	conn := v.(*connection)
	require.Equal(t, valkey.ClientModeCluster, conn.client.Mode())
	require.Len(t, conn.client.Nodes(), len(clusterPorts))

	ctx := t.Context()

	// Enough keys to land on every shard, which the test checks rather than
	// assumes.
	var keys []string
	shards := map[int]bool{}
	for i := range 30 {
		key := "key:" + strconv.Itoa(i)
		keys = append(keys, key)

		slot, err := conn.Do(ctx, conn.B().ClusterKeyslot().Key(key).Build()).AsInt64()
		require.NoError(t, err)
		shards[shardOf(slot)] = true

		require.NoError(t, conn.Set(ctx, key, []byte(key), nil))
	}
	require.Len(t, shards, len(clusterPorts))

	t.Run("MGet fetches keys across shards, in order", func(t *testing.T) {
		entries, err := conn.MGet(ctx, append([]string{"missing"}, keys...))
		require.NoError(t, err)

		require.Len(t, entries, len(keys))
		for i, entry := range entries {
			assert.Equal(t, Entry{Key: keys[i], Value: []byte(keys[i])}, entry)
		}
	})

	t.Run("Scan walks every shard", func(t *testing.T) {
		found, err := conn.Scan(ctx, "key:*", nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, keys, found)

		found, err = conn.Scan(ctx, "key:*", &OptionsScan{Limit: 5})
		require.NoError(t, err)
		assert.Len(t, found, 5)
	})

	// Each of these sends its commands to the node owning the key, which knows
	// none of the scripts until it is first sent one.
	t.Run("the counter and the comparisons reach the key's shard", func(t *testing.T) {
		for _, key := range keys {
			value, err := conn.IncrementTTL(ctx, "counter:"+key, 2, time.Minute)
			require.NoError(t, err)
			assert.Equal(t, int64(2), value)

			set, err := conn.CompareAndSet(ctx, key, []byte(key), []byte("answer"), time.Minute)
			require.NoError(t, err)
			assert.True(t, set, key)

			deleted, err := conn.CompareAndDelete(ctx, key, []byte("answer"))
			require.NoError(t, err)
			assert.True(t, deleted, key)

			require.NoError(t, conn.Set(ctx, key, []byte(key), nil))
		}
	})

	// A set indexing keys of every shard lives on one shard itself; its members are
	// read back across shards through MGet, which is how a group is listed without
	// Scan.
	t.Run("a set indexes keys across shards", func(t *testing.T) {
		added, err := conn.SAdd(ctx, "index", keys, &OptionsSAdd{TTL: time.Minute})
		require.NoError(t, err)
		assert.Equal(t, int64(len(keys)), added)

		members, err := conn.SMembers(ctx, "index")
		require.NoError(t, err)
		assert.ElementsMatch(t, keys, members)

		entries, err := conn.MGet(ctx, members)
		require.NoError(t, err)
		assert.Len(t, entries, len(keys))

		removed, err := conn.SRem(ctx, "index", keys)
		require.NoError(t, err)
		assert.Equal(t, int64(len(keys)), removed)
	})

	t.Run("Delete deletes keys across shards", func(t *testing.T) {
		require.NoError(t, conn.Delete(ctx, append(keys, "missing")))

		entries, err := conn.MGet(ctx, keys)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}
