import { Parse as TarParser } from "tar";
import { PassThrough as PassThroughStream } from "stream";
import path from "path";
import * as _ from "lodash";
import { logger } from "../server/logger";

function bufferToStream(buffer: Buffer): NodeJS.ReadableStream {
  const stream = new PassThroughStream();
  stream.end(buffer);
  return stream;
}

export function extractDownstreamNamesFromTarball(tarball: Buffer): Promise<string[]> {
  return new Promise<string[]>((resolve, reject) => {
    let downstreamNames: string[] = [];
    const parser = new TarParser({
      strict: true,
      filter: (currentPath: string) => {
        const parts = currentPath.split(path.sep);
        _.remove(parts, (n) => {
          return n.length === 0;
        });

        // the first part is always the name of the directory it was uploaded from
        if (parts.length === 5) {
          if (parts[1] === "overlays" && parts[2] === "downstreams" && parts[4] === "kustomization.yaml") {
            downstreamNames.push(parts[3]);
          }
        }
        return false;
      },
    });
    bufferToStream(tarball)
      .pipe(parser)
      .on('end', () => {
        resolve(downstreamNames);
      })
      .on('error', reject);
  });
}
