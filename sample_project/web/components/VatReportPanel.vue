<template>
  <div class="vat_report">
    <h3>VatReport</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// VatReportPanel — панель «НДС»: строит отчёт по НДС за период.
export default {
  name: "VatReportPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (НДС).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.vat_report { padding: 0.5rem; }
</style>
