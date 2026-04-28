#!/bin/bash

# DNS Resolution Testing Script for clawcolony.com
# Addresses issue #68: DNS resolution failure

DOMAIN="clawcolony.com"
LOG_FILE="dns-test-$(date +%Y%m%d-%H%M%S).log"

echo "DNS Resolution Test for $DOMAIN"
echo "Test started at: $(date)"
echo "=====================================" | tee -a $LOG_FILE

# Function to test DNS resolution
test_dns_resolution() {
    local dns_server=$1
    local test_name=$2
    
    echo "Testing $test_name..." | tee -a $LOG_FILE
    
    # Test basic DNS lookup
    if dig @${dns_server} ${DOMAIN} +short > /dev/null 2>&1; then
        echo "✓ DNS resolution: SUCCESS" | tee -a $LOG_FILE
        echo "IP Addresses:" | tee -a $LOG_FILE
        dig @${dns_server} ${DOMAIN} +short | tee -a $LOG_FILE
        
        # Test full domain resolution
        echo "Full domain info:" | tee -a $LOG_FILE
        dig @${dns_server} ${DOMAIN} | head -10 | tee -a $LOG_FILE
    else
        echo "✗ DNS resolution: FAILED" | tee -a $LOG_FILE
        echo "Error details:" | tee -a $LOG_FILE
        dig @${dns_server} ${DOMAIN} 2>&1 | tee -a $LOG_FILE
        return 1
    fi
    echo "" | tee -a $LOG_FILE
}

# Function to test HTTP connectivity
test_http_connectivity() {
    local dns_server=$1
    local test_name=$2
    
    echo "Testing HTTP connectivity via $dns_server..." | tee -a $LOG_FILE
    
    # Get IP first
    ip=$(dig @${dns_server} ${DOMAIN} +short 2>/dev/null)
    if [ -z "$ip" ]; then
        echo "✗ Cannot get IP address via $dns_server" | tee -a $LOG_FILE
        return 1
    fi
    
    # Test HTTP to the IP
    if curl -s --connect-timeout 10 https://${ip} > /dev/null 2>&1; then
        echo "✓ HTTP connectivity: SUCCESS" | tee -a $LOG_FILE
        echo "Response code: $(curl -s -o /dev/null -w "%{http_code}" https://${ip})" | tee -a $LOG_FILE
    else
        echo "✗ HTTP connectivity: FAILED" | tee -a $LOG_FILE
        return 1
    fi
    echo "" | tee -a $LOG_FILE
}

# Test with default DNS
echo "1. Testing with default DNS configuration" | tee -a $LOG_FILE
test_dns_resolution "" "Default DNS"
test_http_resolution "" "Default DNS"

# Test with Google DNS
echo "2. Testing with Google DNS (8.8.8.8)" | tee -a $LOG_FILE
test_dns_resolution "8.8.8.8" "Google DNS"
test_http_connectivity "8.8.8.8" "Google DNS"

# Test with Cloudflare DNS
echo "3. Testing with Cloudflare DNS (1.1.1.1)" | tee -a $LOG_FILE
test_dns_resolution "1.1.1.1" "Cloudflare DNS"
test_http_connectivity "1.1.1.1" "Cloudflare DNS"

# Test with Cloudflare DNS (alternative)
echo "4. Testing with Cloudflare DNS (1.0.0.1)" | tee -a $LOG_FILE
test_dns_resolution "1.0.0.1" "Cloudflare DNS (alt)"
test_http_connectivity "1.0.0.1" "Cloudflare DNS (alt)"

# Test local DNS cache
echo "5. Testing local DNS cache" | tee -a $LOG_FILE
if command -v systemd-resolved > /dev/null; then
    echo "systemd-resolved detected, checking status..." | tee -a $LOG_FILE
    systemctl status systemd-resolved | tee -a $LOG_FILE
elif command -v mDNSResponder > /dev/null; then
    echo "mDNSResponder detected (macOS)" | tee -a $LOG_FILE
else
    echo "No specific DNS cache service detected" | tee -a $LOG_FILE
fi

# Summary
echo "=====================================" | tee -a $LOG_FILE
echo "Test completed at: $(date)" | tee -a $LOG_FILE
echo "Full log saved to: $LOG_FILE" | tee -a $LOG_FILE

# Count failures
failed_tests=$(grep -c "✗" $LOG_FILE)
total_tests=$(grep -c "Testing" $LOG_FILE)

echo "Summary: $failed_tests out of $total_tests tests failed" | tee -a $LOG_FILE

if [ $failed_tests -gt 0 ]; then
    echo "⚠️  DNS resolution issues detected. See full log for details." | tee -a $LOG_FILE
    exit 1
else
    echo "✅ All DNS resolution tests passed." | tee -a $LOG_FILE
    exit 0
fi