#!/bin/bash
# A script to provide a quick summary of the system status for macOS.

echo "--- System Information ---"
echo "Date: $(date)"
echo ""
echo "--- Uptime ---"
uptime
echo ""
echo "--- Memory Usage ---"
vm_stat | perl -ne '/page size of (\d+)/ and $size=$1; /Pages free:\s+(\d+)/ and printf "Free: %.2f GB\n", $1 * $size / 1024**3'
echo ""
echo "--- Disk Usage ---"
df -h /
