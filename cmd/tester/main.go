package main

import (
	"context"
	"flag"
	"log/slog"
	"math/rand/v2"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	bradmemcache "github.com/bradfitz/gomemcache/memcache"
	"github.com/pior/memcache"
	"github.com/pior/memcache/protocol"
)

var (
	flagBrad    = flag.Bool("brad", false, "Enable testing for Brad Fitzpatrick's memcache")
	flagCount   = flag.Int("count", 1000_000, "Number of commands to execute [Default: 1000_000]")
	flagBatch   = flag.Int("batch", 10, "Batch size for pipelining [Default: 10]")
	flagCommand = flag.String("command", "get", "Command to execute [Default: get] [Values: random get set delete increment decrement noop debug]")
)

func main() {
	ctx := context.Background()
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)

	defer cancel()

	flag.Parse()

	err := run(ctx)
	if err != nil {
		slog.Error("Stopped with error", "error", err)
	}
}

func run(ctx context.Context) error {
	servers := memcache.GetMemcacheServers()

	client, err := memcache.NewClient(servers, nil)
	if err != nil {
		return err
	}

	defer func() {
		err := client.Close()
		slog.Error("closing client", "error", err)
	}()

	err = client.ExecuteWait(ctx, memcache.NewSetCommand("testing-found", []byte("value"), time.Minute))
	if err != nil {
		return err
	}

	reporter := NewReporter(ctx, "memcache", 1)
	defer reporter.Stop()

	if *flagBrad {
		clientBrad := bradmemcache.New(servers...)
		return workerBrad(ctx, clientBrad, reporter)
	}

	return workerPipeline(ctx, client, reporter)
}

func commandMaker() func() *protocol.Command {
	switch *flagCommand {
	case "get":
		return func() *protocol.Command {
			return memcache.NewGetCommand("testing-notfound")
		}
	case "set":
		return func() *protocol.Command {
			return memcache.NewSetCommand("testing-found", []byte("value"), time.Minute)
		}
	case "delete":
		return func() *protocol.Command {
			return memcache.NewDeleteCommand("testing-found")
		}

	case "random":
		return func() *protocol.Command {
			switch rand.IntN(10) {
			case 0, 1:
				return memcache.NewGetCommand("testing-found")
			case 2, 3:
				return memcache.NewSetCommand("testing-found", []byte("value"), time.Minute)
			case 4, 5:
				return memcache.NewDeleteCommand("testing-found")
			case 6:
				return memcache.NewIncrementCommand("testing-found", 1)
			case 7:
				return memcache.NewDecrementCommand("testing-found", 1)
			case 8:
				return memcache.NewNoOpCommand()
			case 9:
				return memcache.NewDebugCommand("testing-found")
			default:
				panic("not reachable")
			}
		}
	default:
		panic("Unknown command: " + *flagCommand)
	}
}

// ./tester  2.51s user 6.01s system 86% cpu 9.859 total
func workerPipeline(ctx context.Context, client *memcache.Client, reporter *Reporter) error {
	var err error
	var cmds = make([]*protocol.Command, *flagBatch)
	makeCommand := commandMaker()

	for range *flagCount / *flagBatch {
		for i := range *flagBatch {
			cmds[i] = makeCommand()
		}

		err = client.ExecuteWait(ctx, cmds...)
		if err != nil {
			return err
		}

		reporter.Tick(int64(len(cmds)))

	}
	return nil
}

// Get Miss
// [memcache] Current rate: 49654.52 op/s (info: <nil>)
// [memcache] Current rate: 49422.97 op/s (info: <nil>)
// [memcache] Current rate: 49988.86 op/s (info: <nil>)
// [memcache] Final rate: 49778.23 op/s - 49778.23 op/s per worker - 20.089µs per operation (last info: <nil>)
// Get Hit
// [memcache] Current rate: 48794.49 op/s (info: <nil>)
// [memcache] Current rate: 47737.76 op/s (info: <nil>)
// [memcache] Current rate: 50739.92 op/s (info: <nil>)
// [memcache] Current rate: 49011.50 op/s (info: <nil>)
// [memcache] Final rate: 49088.93 op/s - 49088.93 op/s per worker - 20.371µs per operation (last info: <nil>)
// Set one key
// [memcache] Current rate: 49457.67 op/s (info: <nil>)
// [memcache] Current rate: 49886.75 op/s (info: <nil>)
// [memcache] Current rate: 50049.14 op/s (info: <nil>)
// [memcache] Current rate: 50140.93 op/s (info: <nil>)
// [memcache] Final rate: 49891.46 op/s - 49891.46 op/s per worker - 20.043µs per operation (last info: <nil>)
// Set different key
// [memcache] Current rate: 48878.61 op/s (info: <nil>)
// [memcache] Current rate: 49079.05 op/s (info: <nil>)
// [memcache] Current rate: 48633.30 op/s (info: <nil>)
// [memcache] Current rate: 48885.75 op/s (info: <nil>)
// [memcache] Final rate: 48882.79 op/s - 48882.79 op/s per worker - 20.457µs per operation (last info: <nil>)

// ./tester  1.07s user 2.81s system 45% cpu 8.489 total
func workerBrad(ctx context.Context, client *bradmemcache.Client, reporter *Reporter) error {
	var err error
	// var resp bradmemcache.Item

	var counter int

	for range *flagCount / 1000 {
		for range 1000 {
			counter++
			err = client.Set(&bradmemcache.Item{Key: "testing-" + strconv.Itoa(counter), Value: []byte("value")})
			if err != nil {
				return err
			}
			reporter.Tick(1)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return nil
}
