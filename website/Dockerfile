# syntax=docker/dockerfile:1
# Build the documentation site and serve it with hog-static (dogfooding the
# gateway). Build context is the `website/` directory:
#   docker build -t hog-docs website/
# Requires the `hog-static` image to be built and tagged first (see build/).
#
# BASE must be declared before the first FROM (global scope) so it's available
# to resolve the second FROM's image reference below.
ARG BASE=hog-static

FROM python:3.12-slim AS build
WORKDIR /site
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY mkdocs.yml .
COPY docs/ docs/
# No .git in this stage, so the git-history dates plugin would warn on every
# page and --strict would fail the build. Link and nav checking stays on.
# GitPython raises at import when there is no git binary, before the plugin's
# own enabled flag is ever read, so it is silenced as well.
ENV MKDOCS_GIT_DATES=false \
    GIT_PYTHON_REFRESH=quiet
RUN mkdocs build --strict -d /out

FROM ${BASE}
# hog-static serves /srv/web as an SPA; drop the built site in over the placeholder.
COPY --from=build --chown=hog:hog /out/ /srv/web/
