import os
import re

deploy_dir = '/home/darshan/repositories/kubernetes-test-application/cloud-ecommerce/kubernetes/deployments'

for filename in os.listdir(deploy_dir):
    if filename.endswith('.yaml'):
        filepath = os.path.join(deploy_dir, filename)
        with open(filepath, 'r') as f:
            content = f.read()
        
        # Replace replicas (if > 2)
        content = re.sub(r'replicas:\s*[3-9]+', 'replicas: 2', content)
        
        # Replace cpu requests and limits
        content = re.sub(r'cpu:\s*"[^"]+"', 'cpu: "100m"', content)
        
        # Replace memory requests and limits
        content = re.sub(r'memory:\s*"[^"]+"', 'memory: "128Mi"', content)
        
        with open(filepath, 'w') as f:
            f.write(content)

print("Updated all YAML files.")
