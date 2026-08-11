---
name: domain-intel
description: "Passive domain reconnaissance: WHOIS, DNS, SSL, subdomain discovery — no API keys."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [domain, dns, whois, ssl, reconnaissance, security, osint]
  related_skills: [web_fetch, scrapling]
---

# Domain Intel — Passive Domain Reconnaissance

Gather information about domains without touching the target: WHOIS
records, DNS resolution, SSL certificates, and subdomains.

## When to Use

- "what's the WHOIS for example.com?"
- "list subdomains of example.com"
- "check the SSL cert for..."
- "resolve all DNS records for..."
- "investigate this domain"

## WHOIS Lookup

```python
import whois

domain = whois.whois("example.com")
print(f"Registrar: {domain.registrar}")
print(f"Created: {domain.creation_date}")
print(f"Expires: {domain.expiration_date}")
print(f"Name Servers: {domain.name_servers}")
```

## DNS Records

```python
import dns.resolver

domain = "example.com"
types = ["A", "AAAA", "MX", "NS", "TXT", "CNAME", "SOA"]

for t in types:
    try:
        answers = dns.resolver.resolve(domain, t)
        for r in answers:
            print(f"{t}: {r.to_text()}")
    except dns.resolver.NoAnswer:
        print(f"{t}: No record")
    except Exception as e:
        print(f"{t}: {e}")
```

## SSL Certificate Inspection

```python
import ssl, socket, datetime

hostname = "example.com"
ctx = ssl.create_default_context()
with ctx.wrap_socket(socket.socket(), server_hostname=hostname) as s:
    s.settimeout(5)
    s.connect((hostname, 443))
    cert = s.getpeercert()

print(f"Issued to: {dict(x[0] for x in cert['subject'])}")
print(f"Issued by: {dict(x[0] for x in cert['issuer'])}")
print(f"Valid from: {cert['notBefore']}")
print(f"Valid until: {cert['notAfter']}")
print(f"SAN: {', '.join(x[1] for x in cert['subjectAltName'])}")

# Days until expiry
expiry = datetime.datetime.strptime(cert['notAfter'], "%b %d %H:%M:%S %Y %Z")
days_left = (expiry - datetime.datetime.now()).days
print(f"Days until expiry: {days_left}")
```

## Subdomain Discovery

```python
# Using Certificate Transparency logs
import requests

def crtsh_subdomains(domain):
    url = f"https://crt.sh/?q=%.{domain}&output=json"
    resp = requests.get(url, timeout=30)
    if resp.status_code != 200:
        return []
    
    subs = set()
    for entry in resp.json():
        name = entry.get("name_value", "")
        for n in name.split("\n"):
            n = n.strip().lstrip("*.")
            if n and not n.startswith("*"):
                subs.add(n)
    return sorted(subs)

print("\n".join(crtsh_subdomains("example.com")))
```

## Reverse DNS

```python
import socket

ip = "93.184.216.34"
try:
    hostname = socket.gethostbyaddr(ip)
    print(f"{ip} → {hostname[0]}")
except socket.herror:
    print("No reverse DNS record")
```

## Bulk Domain Analysis

```python
domains = ["example.com", "github.com", "google.com"]

for domain in domains:
    try:
        # DNS check
        answers = dns.resolver.resolve(domain, "A")
        ips = [r.to_text() for r in answers]
        
        # WHOIS
        w = whois.whois(domain)
        registrar = w.registrar or "Unknown"
        
        # SSL
        try:
            ctx = ssl.create_default_context()
            with ctx.wrap_socket(socket.socket(), server_hostname=domain) as s:
                s.settimeout(3); s.connect((domain, 443))
                cert = s.getpeercert()
                issuer = dict(x[0] for x in cert['issuer']).get('organizationName', '?')
        except:
            issuer = "No SSL"
        
        print(f"{domain}: {', '.join(ips)} | {registrar} | SSL: {issuer}")
    except Exception as e:
        print(f"{domain}: Error - {e}")
```
