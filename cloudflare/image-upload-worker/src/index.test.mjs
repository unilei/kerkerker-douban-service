import assert from "node:assert/strict";
import test from "node:test";

import worker from "./index.js";

function createEnv(token = "upload-secret") {
  const writes = [];
  return {
    UPLOAD_TOKEN: token,
    IMAGES: {
      async put(key, body, options) {
        writes.push({
          key,
          body: new Uint8Array(await new Response(body).arrayBuffer()),
          options,
        });
      },
    },
    writes,
  };
}

test("uploads an authenticated image into the bound R2 bucket", async () => {
  const env = createEnv();
  const response = await worker.fetch(
    new Request("https://worker.example/objects/douban/poster.jpg", {
      method: "PUT",
      headers: {
        Authorization: "Bearer upload-secret",
        "Content-Type": "image/jpeg",
        "Cache-Control": "public, max-age=31536000, immutable",
      },
      body: "fake-jpeg",
    }),
    env,
  );

  assert.equal(response.status, 201);
  assert.equal(env.writes.length, 1);
  assert.equal(env.writes[0].key, "douban/poster.jpg");
  assert.equal(new TextDecoder().decode(env.writes[0].body), "fake-jpeg");
  assert.deepEqual(env.writes[0].options.httpMetadata, {
    contentType: "image/jpeg",
    cacheControl: "public, max-age=31536000, immutable",
  });
});

test("rejects uploads without the configured bearer token", async () => {
  const env = createEnv();
  const response = await worker.fetch(
    new Request("https://worker.example/objects/douban/poster.jpg", {
      method: "PUT",
      headers: {"Content-Type": "image/jpeg"},
      body: "fake-jpeg",
    }),
    env,
  );

  assert.equal(response.status, 401);
  assert.equal(env.writes.length, 0);
});

test("rejects non-image content", async () => {
  const env = createEnv();
  const response = await worker.fetch(
    new Request("https://worker.example/objects/douban/not-image.txt", {
      method: "PUT",
      headers: {
        Authorization: "Bearer upload-secret",
        "Content-Type": "text/plain",
      },
      body: "not-an-image",
    }),
    env,
  );

  assert.equal(response.status, 415);
  assert.equal(env.writes.length, 0);
});
