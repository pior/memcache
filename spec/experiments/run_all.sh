#!/bin/bash
# Run all experiments and capture their output

set -e

# Ensure memcached is running
if ! nc -z 127.0.0.1 11211 2>/dev/null; then
    echo "Error: memcached is not running on 127.0.0.1:11211"
    echo "Please start it with: docker run -d --name memcached-test -p 11211:11211 memcached:1.6"
    exit 1
fi

echo "Running all experiments and capturing output..."
echo

# Array of experiment files
experiments=(
    "01_metaget_basic.go"
    "02_metaset_modes.go"
    "03_metadelete.go"
    "04_metaarithmetic.go"
    "05_edge_cases.go"
    "06_protocol_edge_cases.go"
)

# Run each experiment and capture output
for exp in "${experiments[@]}"; do
    if [ -f "$exp" ]; then
        output_file="${exp%.go}.output.txt"
        echo "Running $exp -> $output_file"
        go run "$exp" > "$output_file" 2>&1
        echo "  ✓ Completed"
    else
        echo "  ⚠ File not found: $exp"
    fi
done

echo
echo "All experiments completed!"
echo "Output files created:"
ls -1 *.output.txt
