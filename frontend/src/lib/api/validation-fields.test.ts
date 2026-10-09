import { AxiosError } from "axios";
import api from "./transport";
import { apiValidationFields } from "./validation-fields";

const fields = {
  source_matchers: { name: "sourceMatchers", label: "Source matchers" },
};

it("retains validation fields through the actual transport interceptor and renders safe local labels", async () => {
  const error = await api
    .post(
      "/admin/alerting/inhibitions/",
      {},
      {
        adapter: async (config) => {
          throw new AxiosError(
            "private-fixture",
            "ERR_BAD_REQUEST",
            config,
            undefined,
            {
              config,
              status: 422,
              statusText: "Unprocessable Entity",
              headers: {},
              data: {
                error: {
                  code: "validation_error",
                  message: "private-fixture",
                  fields: [
                    {
                      field: "source_matchers",
                      rule: "invalid_regex",
                      message: "private-fixture",
                    },
                    {
                      field: "password",
                      rule: "required",
                      message: "private-fixture",
                    },
                  ],
                },
              },
            },
          );
        },
      },
    )
    .catch((error: unknown) => error);
  expect(apiValidationFields(error, fields)).toEqual([
    {
      name: "sourceMatchers",
      message: "Source matchers contain an invalid regular expression.",
    },
  ]);
});

it("bounds/deduplicates fields and ignores arbitrary upstream field names and non-validation errors", () => {
  const structured = (status: number, data: unknown) => ({
    response: { status, data },
  });
  expect(
    apiValidationFields(
      structured(500, { error: { code: "validation_error", fields: [] } }),
      fields,
    ),
  ).toEqual([]);
  expect(
    apiValidationFields(
      structured(422, {
        error: {
          code: "validation_error",
          fields: [
            { field: "__proto__", rule: "invalid_regex" },
            null,
            { field: 7 },
            ...Array.from({ length: 40 }, () => ({
              field: "source_matchers",
              rule: "invalid_regex",
              message: "private-fixture",
            })),
          ],
        },
      }),
      fields,
    ),
  ).toHaveLength(1);
  expect(
    apiValidationFields(
      structured(422, {
        error: { code: "validation_error", fields: "private-fixture" },
      }),
      fields,
    ),
  ).toEqual([]);
});
