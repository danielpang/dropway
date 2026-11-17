// SPDX-License-Identifier: FSL-1.1-Apache-2.0

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Deletion may commit even if its response is lost. Verify before retrying. */
export async function deleteAndConfirm(
  dw,
  siteId,
  { attempts = 3, delay = pause } = {},
) {
  let failure;
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      await dw.sites.delete(siteId);
    } catch (error) {
      if (error.status === 404) return;
      failure = error;
    }
    try {
      await dw.sites.get(siteId);
      failure ??= new Error(`Test site ${siteId} still exists after deletion`);
    } catch (error) {
      if (error.status === 404) return;
      failure = error;
    }
    if (attempt + 1 < attempts) await delay(1000 * (attempt + 1));
  }
  throw failure;
}

/** Keep the lifecycle failure and a cleanup failure visible together. */
export async function withTestSite(dw, input, exercise) {
  const site = await dw.sites.create(input);
  let failure;
  try {
    await exercise(site);
  } catch (error) {
    failure = error;
    throw error;
  } finally {
    try {
      await deleteAndConfirm(dw, site.id);
      console.log(`deleted test site ${site.slug ?? site.id}`);
    } catch (cleanupError) {
      if (failure)
        throw new AggregateError(
          [failure, cleanupError],
          "Lifecycle and site cleanup both failed",
        );
      throw cleanupError;
    }
  }
  return site;
}

/** A workflow fallback handles interruption and lost create responses. Never
 * sweep by prefix: only this run attempt's two exact names are eligible. */
export async function cleanupRunSites(dw, slugs) {
  if (
    slugs.length !== 2 ||
    !/^sdk-e2e-\d+-\d+$/.test(slugs[0]) ||
    !/^cli-e2e-\d+-\d+$/.test(slugs[1])
  ) {
    throw new Error(
      "Cleanup requires the SDK and CLI slugs for this run attempt",
    );
  }
  const sites = await dw.sites.list();
  const failures = [];
  for (const site of sites.filter((site) => slugs.includes(site.slug))) {
    try {
      await deleteAndConfirm(dw, site.id);
      console.log(`cleaned up ${site.slug}`);
    } catch (error) {
      failures.push(error);
    }
  }
  if (failures.length)
    throw new AggregateError(failures, "Could not remove all test sites");
}
