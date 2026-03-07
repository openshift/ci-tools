Rebalancer
==========

Rebalancer is a tool used to rebalance jobs between specific cluster profiles.
This tool is needed when Boskos leases are in short supply for some profiles.

Usage
-----

```bash
oc --context app.ci whoami -t > /tmp/token
# go to release repository folder and execute:
/path/to/rebalancer --profiles='azure4,azure-2' --prometheus-bearer-token-path=/tmp/token
``` 
