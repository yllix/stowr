# Installation

Stowr is a single binary and can be used as standalone executable. However, it is also available as a [Docker](https://www.docker.com) image. The installation and first time setup is quite straightforward independently of which system you use.

## Binary

Download the archive for your platform from the [releases page](https://github.com/yllix/stowr/releases), extract it and run:

```sh
tar -xzf linux-amd64-stowr.tar.gz
./stowr -r /path/to/your/files
```

On Windows, extract the `.zip` and run `stowr.exe -r C:\path\to\your\files`.

Stowr is now up and running. Read the ["First Boot"](#first-boot) section for more information.

## Docker

Stowr's Docker image is published on [GitHub Container Registry](https://github.com/yllix/stowr/pkgs/container/stowr) for `linux/amd64`, `linux/arm64` and `linux/arm/v7`. It is a small Alpine image.

```sh
docker run \
    -v filebrowser_data:/srv \
    -v filebrowser_database:/database \
    -v filebrowser_config:/config \
    -p 8080:80 \
    ghcr.io/yllix/stowr
```

Where `filebrowser_data`, `filebrowser_database` and `filebrowser_config` are Docker [volumes](https://docs.docker.com/engine/storage/volumes/), where the data, database and configuration will be stored, respectively. The default configuration and database will be automatically initialized.

The default user that runs Stowr inside the container has UID 1000 and GID 1000. If, for one reason or another, you want to run the Docker container with a different user, please consult Docker's [user documentation](https://docs.docker.com/engine/containers/run/#user).

> [!NOTE]
>
> When using [bind mounts](https://docs.docker.com/engine/storage/bind-mounts/), that is, when you mount a path on the host in the container, you must manually ensure that they have the correct **permissions**. Docker does not do this automatically for you. The host directories must be readable and writable by the user running inside the container. You can use the [`chown`](https://linux.die.net/man/1/chown) command to change the owner of those paths.

Stowr is now up and running. Read the ["First Boot"](#first-boot) section for more information.

## First Boot

Your instance is now up and running. Stowr will automatically bootstrap a database, in which the configuration and the users are stored. You can find the address in which your instance is running, as well as the randomly generated password for the user `admin`, in the console logs.

> [!WARNING]
>
> The automatically generated password for the user `admin` is only displayed once. If you fail to remember it, you will need to manually delete the database and start Stowr again.

Although this is the fastest way to bootstrap an instance, we recommend you to take a look at other possible options, by checking [`config init`](cli/filebrowser-config-init.md) and [`config set`](cli/filebrowser-config-set.md), to make the installation as safe and customized as it can be.

If your goal is to have a public-facing deployment, we recommend taking a look at the [deployment](deployment.md) page for more information on how you can secure your installation.
