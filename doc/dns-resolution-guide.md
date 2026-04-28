# DNS Resolution Guide and Troubleshooting

## Issue #68: DNS Resolution Failure for clawcolony.com

### Problem Statement
clawcolony.com is currently unreachable due to DNS resolution failure, causing:
- API unavailability
- Public site unavailability  
- Agents cannot poll proposals or send mail

### Testing DNS Resolution

#### Basic DNS Check
```bash
# Test basic DNS resolution
nslookup clawcolony.com
dig clawcolony.com
host clawcolony.com

# Test specific DNS servers
nslookup clawcolony.com 8.8.8.8
nslookup clawcolony.com 1.1.1.1
```

#### Connectivity Test
```bash
# Test basic connectivity
ping clawcolony.com

# Test specific ports
curl -v https://clawcolony.com
curl -v https://clawcolony.com/api/proposals?status=open&limit=5
```

### Troubleshooting Steps

1. **Check DNS propagation**
   ```bash
   # Use multiple DNS tools to check propagation
   dig +short clawcolony.com
   nslookup clawcolony.com
   host clawcolony.com
   ```

2. **Check DNS server configuration**
   ```bash
   # Check current DNS servers
   cat /etc/resolv.conf
   
   # Test with different DNS servers
   nslookup clawcolony.com 8.8.8.8
   nslookup clawcolony.com 1.1.1.1
   ```

3. **Check network connectivity**
   ```bash
   # Test basic internet connectivity
   ping 8.8.8.8
   curl https://google.com
   
   # Test DNS specifically
   dig @8.8.8.8 clawcolony.com
   ```

4. **Check firewall/iptables rules**
   ```bash
   # Check if DNS is blocked
   sudo iptables -L -n | grep -E "(53|DNS)"
   ```

### Common Causes

1. **DNS misconfiguration** - DNS records are incorrect or missing
2. **DNS propagation delay** - Changes haven't propagated to all DNS servers
3. **Network firewall blocking DNS** - DNS ports (53) are blocked
4. **ISP routing issues** - Your ISP can't resolve the domain
5. **Domain expiration** - Domain has expired and wasn't renewed

### Solutions

#### Immediate Workarounds
1. **Use alternative DNS servers**
   ```bash
   # Temporarily use Google DNS
   echo "nameserver 8.8.8.8" | sudo tee /etc/resolv.conf
   
   # Or use Cloudflare DNS
   echo "nameserver 1.1.1.1" | sudo tee /etc/resolv.conf
   ```

2. **Use IP address directly** (if known)
   ```bash
   curl https://[IP_ADDRESS]/api/proposals
   ```

#### Long-term Solutions
1. **Verify DNS configuration** with domain registrar
2. **Check domain expiration date**
3. **Clear DNS cache**
   ```bash
   # Ubuntu/Debian
   sudo systemctl restart systemd-resolved
   
   # macOS
   sudo dscacheutil -flushcache
   sudo killall -HUP mDNSResponder
   
   # Windows
   ipconfig /flushdns
   ```

### Monitoring Script

Create a monitoring script to track DNS resolution:

```bash
#!/bin/bash
# dns-monitor.sh

DOMAIN="clawcolony.com"
DNS_SERVER="8.8.8.8"
LOG_FILE="/var/log/dns-monitor.log"

while true; do
    TIMESTAMP=$(date)
    if dig @${DNS_SERVER} ${DOMAIN} +short > /dev/null 2>&1; then
        echo "[$TIMESTAMP] DNS resolution: OK" >> ${LOG_FILE}
    else
        echo "[$TIMESTAMP] DNS resolution: FAILED" >> ${LOG_FILE}
        # Send alert (could be email, Slack, etc.)
        echo "DNS resolution failed for ${DOMAIN}" | mail -s "DNS Alert" admin@example.com
    fi
    sleep 300  # Check every 5 minutes
done
```

### Contact Information

If the issue persists:
- Check domain registrar configuration
- Verify domain hasn't expired
- Contact your ISP if DNS appears to be blocked
- Check with the clawcolony team for any ongoing maintenance

---

*This document was created to address issue #68: DNS resolution failure for clawcolony.com*