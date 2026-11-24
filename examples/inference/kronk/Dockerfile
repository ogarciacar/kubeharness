FROM ghcr.io/ardanlabs/kronk:v1.32.8-cpu@sha256:6ce990ef7231062eb982c23976b2ac67058772310f7e7a12dbb0650084d29177

RUN set -eu; d=/kronk/models/unsloth/Qwen3-0.6B-GGUF; mkdir -p "$d"; \
    curl -fL -o "$d/Qwen3-0.6B-Q8_0.gguf" https://huggingface.co/unsloth/Qwen3-0.6B-GGUF/resolve/50968a4468ef4233ed78cd7c3de230dd1d61a56b/Qwen3-0.6B-Q8_0.gguf; \
    echo "e150ed544dfe6016930c026a93913a5e3184181ebfe6ab2223ae01dd0491784c  $d/Qwen3-0.6B-Q8_0.gguf" | sha256sum -c -
RUN set -eu; d=/kronk/models/bartowski/SmolLM2-135M-Instruct-GGUF; mkdir -p "$d"; \
    curl -fL -o "$d/SmolLM2-135M-Instruct-Q8_0.gguf" https://huggingface.co/bartowski/SmolLM2-135M-Instruct-GGUF/resolve/09816acd5d99df7be770d85ea30822623dab342c/SmolLM2-135M-Instruct-Q8_0.gguf; \
    echo "5a1395716f7913741cc51d98581b9b1228d80987a9f7d3664106742eb06bba83  $d/SmolLM2-135M-Instruct-Q8_0.gguf" | sha256sum -c -
RUN set -eu; d=/kronk/models/ggml-org/gemma-3-270m-it-GGUF; mkdir -p "$d"; \
    curl -fL -o "$d/gemma-3-270m-it-Q8_0.gguf" https://huggingface.co/ggml-org/gemma-3-270m-it-GGUF/resolve/e7647be17ae1108f2f605ed061ca0608b171afff/gemma-3-270m-it-Q8_0.gguf; \
    echo "0ef57d2c838458a1952664260dcba38e5bdda37494f3af732f06e4add24068e3  $d/gemma-3-270m-it-Q8_0.gguf" | sha256sum -c -

COPY --chown=kronk:kronk model_config.yaml /kronk/models/model_config.yaml
COPY --chown=kronk:kronk --chmod=0755 warm.sh /kronk/warm.sh

ENV KRONK_LIB_DOWNLOAD_ENABLED=false \
    KRONK_MEDIA_BACKENDS_ENABLED=false \
    KRONK_ALLOW_UPGRADE=false \
    KRONK_PROCESSOR=cpu \
    KRONK_MALINA_LIB_PATH=/kronk/malina-libraries \
    KRONK_WEB_DEBUG_HOST=0.0.0.0:11445
