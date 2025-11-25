module github.com/pior/memcache/cmd/speed

go 1.25.3

replace github.com/pior/memcache => ../..

replace github.com/pior/memcache/puddle => ../../puddle

require (
	github.com/bradfitz/gomemcache v0.0.0-20250403215159-8d39553ac7cf
	github.com/pior/memcache v0.0.0-20251124194233-813b4f047019
	github.com/pior/memcache/puddle v0.0.0-00010101000000-000000000000
)

require (
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.1.0 // indirect
)
