<template>
  <div class="audit_log">
    <h3>AuditLog</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// AuditLogPanel — панель «аудит»: пишет события в журнал аудита.
export default {
  name: "AuditLogPanel",
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
    // refresh запускает обработку (аудит).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.audit_log { padding: 0.5rem; }
</style>
