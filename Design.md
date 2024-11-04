# Distribute Scheduler Design

## Background
Currently, in the cloud-native scenario, we hope to maximize the utilization of the hybrid deployment of on-demand and spot instances (pure spot clusters still pose risks to availability). In addition, considerations need to be made for single-replica and multi-replica situations. Moreover, to minimize modifications to Kubernetes (k8s) itself, we consider using mutating admission webhooks to dynamically modify the affinity of nodes and pods during API requests, thereby simplifying the design and implementation of the scheduler.

## Design
On-demand and spot nodes can be regarded as nodes with different costs. For example, the cost of an on-demand node is 10, and that of a spot node is 1. Ignoring the resources and computing performance of the two types of nodes, we only focus on minimizing the cost result, that is:

$$PodNum_{on-demand}=PodNum_{spot}=M$$

$$TotalPods=N_{on-demand}*M+N_{spot}*M$$

$$Cost={10}*N_{on-demand}+{1}*N_{spot}$$

PodNum represents the maximum number of pods that a node is expected to carry. TotalPods refers to the total number of pods in the hybrid cluster, and Cost represents the expense of purchasing nodes. TotalPods is the expected number of pods for the business.

An additional condition is that there are different categories in the workload, and at least one node in each category must be online during operation to ensure availability. Combining the above formulas, for single-replica and multi-replica nodes, pods tend to be created on on-demand nodes in the initial stage. When subsequent pods are created, they should be as close to spot nodes as possible. To ensure uniformity, the probability can be fitted using a formula similar to the following:

$$weight=\frac{MAXWEIGHT}{pod^2}*A$$

where MAX_WEIGHT is the expected maximum weight, which is a fixed value, A is a proportionality coefficient, and later it can be considered to expose it to external Custom Resource Definitions (CRDs) and other resource configurations for a certain workload's proportion. pod is the index of the created pod. For example:

```bash
MAX_WEIGHT = 10, A = 1
pod = 1, weight = 10
pod = 2, weight = 2.5
pod = 3, weight = 1 (minimum is 1)
pod = 4, weight = 1 (minimum is 1)
pod = 5, weight = 1 (minimum is 1)
```

### Scheduler
The Design section has analyzed how we can create a series of pod creation strategies based on the existing scenario, that is, adding node affinity and anti-affinity to pods according to the creation order for different workloads. Additionally, the affinity here tends to use preferredDuringSchedulingIgnoredDuringExecution to complete the affinity configuration.

> 1. When introducing this strategy into an existing cluster, it is similar, but the webhook is not very applicable. If we need to go through the webhook again, we also need to pass through the API server. Therefore, for the time being, we will target newly created clusters and introduce this scheduler.
> 2. In the case of multi-replicas, only the form of self-organizing clusters is considered. If there are restrictions on master/slave in the business, manual intervention such as adding nodeSelector labels is required to affect the scheduler.

![pipelin](./images/pipeline.png)

As shown in the figure, after starting the webhook, the scheduler itself will list-watch the status of nodes and pods in the cluster and initialize the current Scheduler cache. When receiving a request to create a new pod, it will serialize/deserialize the request and response in the way of the webhook and fill in the configuration of NodeAffinity internally (PodAffinity is not considered for the time being).

The PodCache maintains the pod status and weight proportion of a certain Deployment/StatefulSet. Newly created nodes will calculate the weight proportion of weight based on the rules in the Design section on this basis.
- Consider the following situations:
  - For normal pod creation without any preceding pods being destroyed, the filling is directly completed according to the index.
  - When a pod is scheduled to a spot node and terminated, the scheduler/API server will fill in the first vacant Pod Weight position when creating a new pod.
  - When the webhook is restarted, consider rebuilding the PodCache from the labels of the existing pods.
The NodeCache maintains the status of on-demand/spot nodes and can be used as an offset value for weight calculation. In a simple version, this NodeCache can actually be ignored.

### Strategy
- CREATE
  - Calculate Affinity: Calculate the weight on on-demand nodes. Calculate the weight according to the index of pod creation. If there is a vacancy in the weight cache due to a pod being deleted, the new pod will directly use that weight.
  - Calculate Anti-affinity: Calculate the weight on spot nodes. The process is the same as above.
```yaml
affinity:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
            preference:
            matchExpressions:
              - key: node.kubernetes.io/capacity
                  operator: In
                  values:
                  - on-demand
     nodeAntiAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            preference:
            matchExpressions:
              - key: node.kubernetes.io/capacity
                  operator: In
                  values:
                  - spot
```
- DELETE
  - The expected deletion logic is determined by the cloud service provider, that is, actively shutting down spot nodes. Therefore, the delete logic of the k8s scheduler itself is temporarily ignored.

### Supplement
Regarding some usage considerations, some other webhook configurations can be provided to support enabling/disabling the corresponding scheduling strategies for deployments/stateful sets, directly using manual configurations.
```bash
cloudpilot.ai/webhook-scheduler=true
cloudpilot.ai/webhook-scheduler=false
```
The implementation method is to directly skip the subsequent labeling process for these labels.

### Take off
This design simplifies the considerations of the Scheduler part. For CREATE requests, it utilizes the enhanced label configuration of the webhook to complete the design and implementation of the scheduler part, hoping that the k8s native scheduler can complete the affinity scheduling based on labels. In terms of calculation rules, it can, to a certain extent, take into account the availability of on-demand devices and the dynamic expansion capacity of spot devices. Additionally, some configurations are provided to dynamically switch the ability of the webhook.

However, regarding the behavior of the cluster itself, such as scaling up/down, migration, etc., the capabilities implemented using webhook may not be strong enough, and it is necessary to consider combining other components to achieve a more fine-grained implementation.

### References
- Node Affinity: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/
- Admission Webhooks: https://kubernetes.io/blog/2019/03/21/a-guide-to-kubernetes-admission-controllers/
- Pod delete Cost: https://kubernetes.io/docs/concepts/workloads/controllers/replicaset/#pod-deletion-cost