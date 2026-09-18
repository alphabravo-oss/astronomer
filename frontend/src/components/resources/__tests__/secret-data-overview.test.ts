import { decodeSecretValue } from "@/components/resources/secret-data-overview";

function encode(value: string): string {
  return btoa(unescape(encodeURIComponent(value)));
}

describe("decodeSecretValue", () => {
  it("returns decoded UTF-8 text instead of base64", () => {
    const decoded = decodeSecretValue(
      encode("database-password"),
      "password",
      "Opaque",
    );

    expect(decoded.kind).toBe("Text");
    expect(decoded.display).toBe("database-password");
    expect(decoded.display).not.toContain(encode("database-password"));
  });

  it("formats JSON and expands docker auth credentials", () => {
    const auth = btoa("robot:correct-horse-battery-staple");
    const decoded = decodeSecretValue(
      encode(JSON.stringify({ auths: { "registry.example": { auth } } })),
      ".dockerconfigjson",
      "kubernetes.io/dockerconfigjson",
    );

    expect(decoded.kind).toBe("JSON");
    expect(decoded.display).toContain('"username": "robot"');
    expect(decoded.display).toContain(
      '"password": "correct-horse-battery-staple"',
    );
    expect(decoded.display).not.toContain(auth);
  });

  it("recognizes PEM and preserves the decoded certificate text", () => {
    const pem =
      "-----BEGIN CERTIFICATE-----\ncertificate\n-----END CERTIFICATE-----\n";
    const decoded = decodeSecretValue(
      encode(pem),
      "tls.crt",
      "kubernetes.io/tls",
    );

    expect(decoded.kind).toBe("PEM");
    expect(decoded.display).toBe(pem);
    expect(decoded.extension).toBe("pem");
  });

  it("classifies non-UTF-8 decoded values as binary", () => {
    const encoded = btoa(String.fromCharCode(0xff, 0x00, 0xfe));
    const decoded = decodeSecretValue(encoded, "archive", "Opaque");

    expect(decoded.kind).toBe("Binary");
    expect(decoded.display).toBeUndefined();
    expect(Array.from(decoded.bytes)).toEqual([0xff, 0x00, 0xfe]);
  });
});
