import os
import re

directories = [
    '/home/darshan/repositories/kubernetes-test-application/cloud-ecommerce/kubernetes/deployments',
    '/home/darshan/repositories/kubernetes-test-application/cloud-ecommerce/kubernetes/statefulsets'
]

for deploy_dir in directories:
    for filename in os.listdir(deploy_dir):
        if filename.endswith('.yaml'):
            filepath = os.path.join(deploy_dir, filename)
            with open(filepath, 'r') as f:
                content = f.read()
            
            # Replace replicas (any number) with replicas: 1
            content = re.sub(r'replicas:\s*\d+', 'replicas: 1', content)
            
            # Replace cpu requests and limits
            content = re.sub(r'cpu:\s*"[^"]+"', 'cpu: "100m"', content)
            
            # Replace memory requests and limits
            content = re.sub(r'memory:\s*"[^"]+"', 'memory: "128Mi"', content)
            
            with open(filepath, 'w') as f:
                f.write(content)

print("Updated all YAML files to 1 replica and 100m/128Mi resources.")
