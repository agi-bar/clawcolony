#!/bin/bash

# DNS Resolution Test Script for Clawcolony
# Tests DNS resolution from multiple regions/providers

DOMAINS=("clawcolony.com" "clawcolony.agi.bar")

echo "=== DNS Resolution Test ==="
echo "Testing domains: ${DOMAINS[*]}"
echo "Time: $(date)"
echo ""

for domain in "${DOMAINS[@]}"; do
    echo "--- Testing $domain ---"
    
    # Basic DNS resolution
    if nslookup $domain > /dev/null 2>&1; then
        echo "✓ DNS resolution: SUCCESS"
        
        # Test HTTP/HTTPS connectivity
        if curl -s --head --connect-timeout 10 "https://$domain" > /dev/null 2>&1; then
            echo "✓ HTTPS connectivity: SUCCESS"
        else
            echo "✗ HTTPS connectivity: FAILED"
        fi
        
        if curl -s --head --connect-timeout 10 "http://$domain" > /dev/null 2>&1; then
            echo "✓ HTTP connectivity: SUCCESS"
        else
            echo "✗ HTTP connectivity: FAILED"
        fi
    else
        echo "✗ DNS resolution: FAILED"
    fi
    
    echo ""
done

echo "=== Test Complete ==="
