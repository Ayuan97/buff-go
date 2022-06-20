
#### 后端


    ```sh
    go mod download
    go build -o qingshang-api .
    ```

启动后端

    ```sh
    chmod +x qingshan-api
    ./qingshan-api
    ```

#### 前端

1. 进入前端目录 `web`，编辑 `.env` 文件中后端服务地址，下载依赖包

    ```sh
    cd ./web
    vim .env
    yarn
    ```

2. 编译前端

    ```sh
    yarn build
    ```

    build完成后，可以在dist目录获取编译产出，配置nginx指向至该目录即可

#### 桌面端

1. 进入前端目录 `web`，编辑 `.env` 文件中后端服务地址，下载依赖包

    ```sh
    cd ./web
    vim .env
    yarn
    ```

2. 编译前端

    ```sh
    yarn build
    ```
   
3. 构建桌面端
   ```sh
   yarn tauri build
   ```
   桌面端是使用[Rust](https://www.rust-lang.org/) + [tauri](https://github.com/tauri-apps/tauri)编写
   的，需要Rust编译环境，具体安装指南请参考[https://www.rust-lang.org/tools/install](https://www.rust-lang.org/tools/install).

### 其他说明

建议后端服务使用 `supervisor` 守护进程，并通过 `nginx` 反向代理后，提供API给前端服务调用。

短信通道使用的[聚合数据](https://www.juhe.cn/)，如果申请不下来，可以考虑替换其他服务商。

