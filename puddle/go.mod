module github.com/pior/memcache/puddle

go 1.25.3

replace github.com/pior/memcache => ..

require (
	github.com/jackc/puddle/v2 v2.2.2
	github.com/pior/memcache v0.0.0-20251124194233-813b4f047019
)

require golang.org/x/sync v0.1.0 // indirect
