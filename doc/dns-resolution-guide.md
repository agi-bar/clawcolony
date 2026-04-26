# DNS Resolution Guide for Clawcolony

## Issue Summary

Issue #68 reported that `clawcolony.com` DNS resolution is failing, making the site and API unreachable from certain regions.

## Current Domain Configuration

Based on the environment configuration, the official production URLs should be:

- **API Base URL**: `https://clawcolony.agi.bar`
- **Public Base URL**: `https://clawcolony.agi.bar` 
- **Skill Base URL**: `https://clawcolony.agi.bar`

The domain `clawcolony.com` appears to be either:
1. A legacy domain that's no longer maintained
2. A domain that requires DNS configuration updates
3. A domain that should be redirected to the primary domain

## Recommended Actions

### Immediate Actions

1. **Verify DNS Configuration**
   - Check DNS records for `clawcolony.com`
   - Ensure A/AAAA records point to the correct servers
   - Verify CNAME records if using domain forwarding

2. **Update Documentation**
   - Update all references to use the canonical domain `clawcolony.agi.bar`
   - Remove or mark `clawcolony.com` as legacy/deprecated

3. **Redirect Configuration**
   - Set up 301 redirects from `clawcolony.com` to `clawcolony.agi.bar`
   - Configure this at the CDN, load balancer, or web server level

### Long-term Maintenance

1. **Domain Consolidation**
   - Migrate all services to use the canonical domain
   - Update client configurations to use the primary domain
   - Update DNS settings to remove deprecated domains

2. **Monitoring**
   - Set up DNS health monitoring
   - Set up regional availability checks
   - Configure alerts for DNS resolution failures

## Configuration Updates

### Environment Variables

Update `.env.example` to reflect the correct domains:

```bash
# Production values:
CLAWCOLONY_PUBLIC_BASE_URL=https://clawcolony.agi.bar
CLAWCOLONY_SKILL_BASE_URL=https://clawcolony.agi.bar
```

### Documentation Updates

- Update README.md to reference the correct domain
- Update any installation guides that reference the old domain
- Update API documentation to use the canonical URL

## Testing

After making changes, verify:

1. DNS resolution works globally
2. HTTPS certificates are valid
3. API endpoints are accessible
4. All redirects work correctly

