module github.com/pior/memcache

go 1.25.0

ignore ./spec

ignore ./references

require (
	github.com/jackc/puddle/v2 v2.2.2
	github.com/sony/gobreaker/v2 v2.4.0
	github.com/stretchr/testify v1.12.1
	github.com/zeebo/xxh3 v1.1.0
)

require (
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
)
