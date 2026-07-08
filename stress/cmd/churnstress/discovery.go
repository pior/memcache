package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"
)

type dynNode struct{ addr, name string }

func dockerRun(name string, port int) error {
	return exec.Command("docker", "run", "-d", "--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:11211", port),
		"memcached:1.6", "-m", "64", "-c", "4096", "-t", "2").Run()
}

// runDiscovery models a service-discovery churn: a steady state of -fleet live
// memcached, continuously rolling — every -redeploy-interval it retires
// -redeploy-batch servers (removes them from the Servers list, then terminates
// the container, like a pod going away) and deploys the same number of fresh
// ones with brand-new identities (new container, new port). The set of distinct
// addresses the client has ever routed to therefore grows monotonically.
//
// This is the realistic k8s rolling-deploy / pod-IP-rotation pattern, and the D2
// probe: the reaper always runs, so the pool count stays ~-fleet rather than
// tracking deployed_total (which would be an unbounded leak of pools, breakers,
// and sockets). A longer -reaper-interval reaps departed pools less promptly, so
// the pool count settles proportionally higher above the live-fleet size.
func runDiscovery(ctx context.Context, ms *mutableServers) {
	setPhase("discovery-warmup")
	live := map[string]dynNode{}
	nextPort := *basePort
	nextID := 0

	deploy := func() dynNode {
		n := dynNode{
			addr: fmt.Sprintf("127.0.0.1:%d", nextPort),
			name: fmt.Sprintf("csdyn-%d", nextID),
		}
		if err := dockerRun(n.name, nextPort); err != nil {
			log.Printf("deploy %s: %v", n.name, err)
		}
		nextID++
		nextPort++
		distinctDeployed.Add(1)
		return n
	}

	publish := func() {
		addrs := make([]string, 0, len(live))
		for a := range live {
			addrs = append(addrs, a)
		}
		ms.set(addrs)
		liveCount.Store(int64(len(addrs)))
	}

	// full cleanup of every container we ever created
	defer func() {
		for id := 0; id < nextID; id++ {
			dockerDo("rm", "-f", fmt.Sprintf("csdyn-%d", id))
		}
	}()

	// initial fleet
	for range *fleet {
		n := deploy()
		live[n.addr] = n
	}
	publish()
	if !sleepCtx(ctx, 5*time.Second) { // let the containers accept connections
		return
	}

	setPhase("discovery-rolling")
	deadline := time.Now().Add(*duration)
	tick := time.NewTicker(*redeployEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if time.Now().After(deadline) {
				return
			}
			// retire redeployBatch live nodes: drop from the Servers list first
			// so no new op routes to them, then terminate the container.
			retired := make([]dynNode, 0, *redeployBatch)
			for a, n := range live {
				if len(retired) >= *redeployBatch {
					break
				}
				retired = append(retired, n)
				delete(live, a)
			}
			// deploy the same number of fresh identities
			for range *redeployBatch {
				n := deploy()
				live[n.addr] = n
			}
			publish()
			for _, n := range retired {
				dockerDo("rm", "-f", n.name)
			}
		}
	}
}
