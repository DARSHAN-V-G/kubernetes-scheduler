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
            
            # Increase initial delay to give pods 90s to boot up
            content = re.sub(r'initialDelaySeconds:\s*\d+', 'initialDelaySeconds: 90', content)
            
            # Inject timeoutSeconds: 15 directly below periodSeconds
            content = re.sub(r'(periodSeconds:\s*\d+)', r'\1\n            timeoutSeconds: 15', content)
            
            with open(filepath, 'w') as f:
                f.write(content)

print("Updated all YAML files with relaxed probe timeouts.")
