import { gzipSync } from "node:zlib";

export function archiveFor(entries) {
  const blocks = [];
  for (const { name, contents, type = "0", mode = 0o644 } of entries) {
    const data = Buffer.from(contents);
    const header = Buffer.alloc(512);
    header.write(name, 0, 100, "utf8");
    header.write(`${mode.toString(8).padStart(7, "0")}\0`, 100, "ascii");
    header.write(
      `${data.length.toString(8).padStart(11, "0")}\0`,
      124,
      "ascii",
    );
    header.fill(32, 148, 156);
    header[156] = type.charCodeAt(0);
    header.write("ustar\0", 257, "binary");
    header.write("00", 263, "ascii");
    const sum = header.reduce((value, byte) => value + byte, 0);
    header.write(`${sum.toString(8).padStart(6, "0")}\0 `, 148, "ascii");
    blocks.push(header, data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks));
}
