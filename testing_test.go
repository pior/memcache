package memcache

import "os"

func GetMemcacheServers() []string {
	return []string{os.Getenv("MEMCACHE_SERVERS")}
}
