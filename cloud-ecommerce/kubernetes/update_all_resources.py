import os
import re

base_dir = os.path.dirname(os.path.abspath(__file__))
directories = [
    os.path.join(base_dir, 'deployments'),
    os.path.join(base_dir, 'statefulsets')
]

for deploy_dir in directories:
    for filename in os.listdir(deploy_dir):
        if filename.endswith('.yaml'):
            filepath = os.path.join(deploy_dir, filename)
            with open(filepath, 'r') as f:
                content = f.read()
            
            # Replace replicas (any number) with replicas: 2
            content = re.sub(r'replicas:\s*\d+', 'replicas: 2', content)
            
            # Do not overwrite resources for critical databases/brokers to avoid OOM
            if filename not in ['mongodb.yaml', 'postgres.yaml', 'kafka.yaml', 'rabbitmq.yaml']:
                # Replace cpu requests and limits
                content = re.sub(r'cpu:\s*"[^"]+"', 'cpu: "100m"', content)
                # Replace memory requests and limits
                content = re.sub(r'memory:\s*"[^"]+"', 'memory: "128Mi"', content)
            
            with open(filepath, 'w') as f:
                f.write(content)

print("Updated all YAML files to 2 replicas (with OOM protection for databases).")
